package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog"
)

type Settings struct {
	Port string `envconfig:"PORT" default:"5000"`
	Host string `envconfig:"HOST" required:"true"`
}

var err error
var log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr})
var settings Settings
var router *mux.Router

func main() {
	log.Debug().Msg("starting app...")

	err = envconfig.Process("", &settings)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("failed when loading environment variablesettings.")
	}
	log.Debug().Msg("...settings loaded.")

	// routes
	router = mux.NewRouter()

	// the redirect.name-like stuff
	http.HandleFunc("/", handle)
	srv := &http.Server{
		Addr:         ":" + settings.Port,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}

	log.Debug().Msg("...routes declared.")

	log.Info().Str("port", settings.Port).Msg("started listening.")
	http.ListenAndServe(":"+settings.Port, srv.Handler)
	log.Info().Msg("exiting...")
}

func handle(w http.ResponseWriter, r *http.Request) {
	log.Debug().
		Str("country", r.Header.Get("Cf-Ipcountry")).
		Str("host", r.Host).
		Str("url", r.URL.String()).
		Str("referrer", r.Header.Get("Referer")).
		Msg("got visit")

	_, euroIP := countries[r.Header.Get("Cf-Ipcountry")]
	var whitelisted bool
	var selfdeclared bool
	if v, err := r.Cookie("__eush:whitelisted"); err == nil {
		whitelisted = v.Value == "y"
	}
	if v, err := r.Cookie("__eush:selfdeclared"); err == nil {
		selfdeclared = v.Value == "y"
	}

	var block bool
	var ask bool
	if euroIP {
		ask = true
	}
	if selfdeclared {
		block = true
	}
	if whitelisted {
		block = false
		ask = false
	}

	handleAsk := func(v string) {
		if v == "n" {
			ask = false
			if euroIP {
				block = true
			}
		} else if v == "y" {
			ask = true
		}
	}

	var target string // to where we will redirect or proxy
	var redirect bool
	var proxy bool

	handleTXT := func() {
		txts, err := net.LookupTXT("_euroshield." + r.Host)
		if err == nil {
			for _, txt := range txts {
				kvs := strings.Split(txt, " ")
				for _, kv := range kvs {
					spl := strings.Split(kv, "=")
					k := spl[0]
					v := spl[1]

					switch k {
					case "ask":
						handleAsk(v)
					case "redirect":
						target = v
						redirect = true
					case "proxy":
						target = v
						proxy = true
					}
				}
			}
		}
	}

	if r.Host == settings.Host {
		// it's a request for this host here indeed
		switch r.URL.Path {
		case "/error":
			fmt.Fprint(w, r.URL.Query().Get("msg"))
			w.WriteHeader(400)
		case "/shield.js":
			// the JS snippet
			handleAsk(r.URL.Query().Get("ask"))

			log.Debug().
				Bool("block", block).Bool("ask", ask).
				Msg("js snippet")

			w.Header().Set("Cache-Control", "no-cache")

			serveJS := func(forceask, forceblock string) {
				askHTML := strings.Replace(`
<div>
  <p>Are you a citizen of the European Union?</p>
  <div class="buttons">
    <button onclick="yes(); return false">Yes</button>
    <button onclick="no(); return false">No</button>
  </div>
</div>
                `, "\n", "", -1)

				blockHTML := strings.Replace(`
<div>
<p>Dear visitor,</p>
<p>We are very sad to announce that our service is incompatible with the <a href="https://en.wikipedia.org/wiki/General_Data_Protection_Regulation">GDPR</a> requirements.</p>
<p>Because of that we prefer to not serve what would be a potentially illegal product to all citizens from any European Union country.</p>
<p>Since you are one of those, you are blocked from viewing this website.</p>
<p>We are very sorry.</p>
</div>
                `, "\n", "", -1)

				fmt.Fprintf(w, `
var link = document.createElement('link')
link.href = 'https://%s/modal.css'
link.rel = 'stylesheet'
var modal = document.createElement('div')
modal.id = 'euroshield-modal'
document.head.appendChild(link)
document.body.appendChild(modal)

var forceask
if (%s) {
  forceask = true
}

var forceblock
if (%s) {
  forceblock = true
}

function yes () {
  localStorage.setItem('_eush:selfdeclared', 'y')
  localStorage.setItem('_eush:whitelist', 'n')
  reload()
}
function no () {
  localStorage.setItem('_eush:selfdeclared', 'n')
  localStorage.setItem('_eush:whitelist', 'y')
  reload()
}
function reload () {
  if (forceblock || localStorage.getItem('_eush:selfdeclared') === 'y') {
    modal.innerHTML = '<div>%s</div>'
  } else if (localStorage.getItem('_eush:whitelist') === 'y') {
    document.body.removeChild(modal)
    document.head.removeChild(link)
  } else {
    modal.innerHTML = '<div>%s</div>'
  }
}

reload()
                `, settings.Host, forceask, forceblock, blockHTML, askHTML)
			}
			if block {
				serveJS("false", "true")
			} else if ask {
				serveJS("false", "false")
			} else {
				// do nothing
				fmt.Fprint(w, "")
			}
		case "/modal.css":
			// CSS for the modal
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, `
#euroshield-modal {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(240, 240, 240, 0.8);
  font-family: monospace;
  font-size: 1.4em;
  z-index: 9999999;
}
#euroshield-modal * {
  box-sizing: border-box;
}
#euroshield-modal > div {
  width: 600px;
  height: 400px;
  position: fixed;
  top: 50%;
  left: 50%;
  margin-top: -220px;
  margin-left: -330px;
  background: #162274;
  background-image: url('https://`+settings.Host+`/eu-flag.jpg');
  background-size: cover;
}
#euroshield-modal > div > div {
  background: rgba(0, 0, 0, 0.7);
  color: #f4f4f4;
  width: 100%;
  height: 100%;
  padding: 5%;
}
#euroshield button {
  cursor: pointer;
}
                `)
		case "/eu-flag.jpg":
			http.ServeFile(w, r, "eu-flag.jpg")
		default:
			// visitor wants to browse us
			http.ServeFile(w, r, "landing/index.html")
		}
	} else {
		// it's a proxy or redirect request
		handleTXT()

		log.Debug().
			Bool("block", block).Bool("ask", ask).
			Bool("proxy", proxy).Bool("redir", redirect).Str("target", target).
			Msg("proxy or redirect")

		if block {
			performBlock(w, r)
		} else if ask {
			performAsk(w, r)
		} else {
			if redirect {
				http.Redirect(w, r, target, 302)
			} else if proxy {
				fmt.Fprint(w, "we should have been proxying requests to "+target)
			}
		}
	}
}

func performBlock(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "You are blocked from accessing this site.")
}

func performAsk(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Are you an european citizen?")
}

var countries = map[string]bool{
	"BE": true,
	"BG": true,
	"CZ": true,
	"DK": true,
	"DE": true,
	"EE": true,
	"IE": true,
	"EL": true,
	"ES": true,
	"FR": true,
	"HR": true,
	"IT": true,
	"CY": true,
	"LV": true,
	"LT": true,
	"LU": true,
	"HU": true,
	"MT": true,
	"NL": true,
	"AT": true,
	"PL": true,
	"PT": true,
	"RO": true,
	"SI": true,
	"SK": true,
	"FI": true,
	"SE": true,
	"UK": true,
}
