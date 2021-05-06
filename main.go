package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wallacy/ssl-proxy/gen"
	"github.com/wallacy/ssl-proxy/reverseproxy"
	"golang.org/x/crypto/acme/autocert"
)

var (
	to              = flag.String("to", "http://127.0.0.1:80", "the address and port for which to proxy requests to")
	fromURL         = flag.String("from", "127.0.0.1:4430", "the tcp address and port this proxy should listen for requests on")
	certFile        = flag.String("cert", "", "path to a tls certificate file. If not provided, ssl-proxy will generate one for you in ~/.ssl-proxy/")
	keyFile         = flag.String("key", "", "path to a private key file. If not provided, ssl-proxy will generate one for you in ~/.ssl-proxy/")
	domain          = flag.String("domain", "", "domain to mint letsencrypt certificates for. Usage of this parameter implies acceptance of the LetsEncrypt terms of service.")
	redirectHTTP    = flag.Int("redirectHTTP", 0, "if set, redirects http requests from provided port to https at your fromURL (0 disable)")
	altnames        = flag.String("altnames", "localhost", "comma separated altnames for the certificate DNS field")
	userHomeDir, _  = os.UserHomeDir()
	defaultCertFile = userHomeDir + "/.ssl-proxy/cert.pem"
	defaultKeyFile  = userHomeDir + "/.ssl-proxy/key.pem"
)

// Prefixes
const (
	HTTPSPrefix = "https://"
	HTTPPrefix  = "http://"
)

func main() {
	flag.Parse()

	validCertFile := *certFile != ""
	validKeyFile := *keyFile != ""
	validDomain := *domain != ""

	// Determine if we need to generate self-signed certs
	if (!validCertFile || !validKeyFile) && !validDomain {
		// Use default file paths
		*certFile = defaultCertFile
		*keyFile = defaultKeyFile

		needCreate := false
		if _, err := os.Stat(*certFile); os.IsNotExist(err) {
			needCreate = true
		} else if _, err := os.Stat(*keyFile); os.IsNotExist(err) {
			needCreate = true
		}

		if needCreate {
			log.Printf("No existing cert or key specified, generating some self-signed certs for use (%s, %s)\n", *certFile, *keyFile)

			// Generate new keys
			certBuf, keyBuf, fingerprint, err := gen.Keys(365*24*time.Hour, strings.Split(*altnames, ","))
			if err != nil {
				log.Fatal("Error generating default keys", err)
			}

			certOut, err := create(*certFile)
			if err != nil {
				log.Fatal("Unable to create cert file", err)
			}
			certOut.Write(certBuf.Bytes())

			keyOut, err := os.OpenFile(*keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			if err != nil {
				log.Fatal("Unable to create the key file", err)
			}
			keyOut.Write(keyBuf.Bytes())

			log.Printf("SHA256 Fingerprint: % X", fingerprint)
		} else {
			log.Printf("Found default cert/key files: using...")
		}
	}

	// Ensure the to URL is in the right form
	if !strings.HasPrefix(*to, HTTPPrefix) && !strings.HasPrefix(*to, HTTPSPrefix) {
		*to = HTTPPrefix + *to
		log.Println("Assuming -to URL is using http://")
	}

	// Parse toURL as a URL
	toURL, err := url.Parse(*to)
	if err != nil {
		log.Fatal("Unable to parse 'to' url: ", err)
	}

	// Setup reverse proxy ServeMux
	p := reverseproxy.Build(toURL)
	mux := http.NewServeMux()
	mux.Handle("/", p)

	log.Printf(green("Proxying calls from https://%s (SSL/TLS) to %s"), *fromURL, toURL)

	// Redirect http requests on port 80 to TLS port using https
	if *redirectHTTP > 0 {
		// Redirect to caller host, unless a domain is specified--in that case, redirect using the public facing
		// domain
		redirectURL := *fromURL
		redirectPort := fmt.Sprintf(":%v", *redirectHTTP)

		redirectTLS := func(w http.ResponseWriter, r *http.Request) {
			if validDomain {
				redirectURL = *domain
			} else {
				redirectURL = r.URL.Hostname()
				if len(redirectURL) <= 0 {
					host, _, err := net.SplitHostPort(r.Host)
					if err == nil {
						redirectURL = host
					} else {
						redirectURL = *fromURL
					}
				}
			}
			http.Redirect(w, r, "https://"+redirectURL+r.RequestURI, http.StatusTemporaryRedirect)
		}
		go func() {
			log.Println(
				fmt.Sprintf("Also redirecting https requests on port %s to https requests on %s", redirectPort, redirectURL))
			err := http.ListenAndServe(redirectPort, http.HandlerFunc(redirectTLS))
			if err != nil {
				log.Println("HTTP redirection server failure")
				log.Println(err)
			}
		}()
	}

	// Determine if we should serve over TLS with autogenerated LetsEncrypt certificates or not
	if validDomain {
		// Domain is present, use autocert
		// TODO: validate domain (though, autocert may do this)
		// TODO: for some reason this seems to only work on :443
		log.Printf("Domain specified, using LetsEncrypt to autogenerate and serve certs for %s\n", *domain)
		if !strings.HasSuffix(*fromURL, ":443") {
			log.Println("WARN: Right now, you must serve on port :443 to use autogenerated LetsEncrypt certs using the -domain flag, this may NOT WORK")
		}
		m := &autocert.Manager{
			Cache:      autocert.DirCache("certs"),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*domain),
		}
		s := &http.Server{
			Addr:      *fromURL,
			TLSConfig: m.TLSConfig(),
		}
		s.Handler = mux
		log.Fatal(s.ListenAndServeTLS("", ""))
	} else {
		// Domain is not provided, serve TLS using provided/generated certificate files
		log.Fatal(http.ListenAndServeTLS(*fromURL, *certFile, *keyFile, mux))
	}

}

// green takes an input string and returns it with the proper ANSI escape codes to render it green-colored
// in a supported terminal.
// TODO: if more colors used in the future, generalize or pull in an external pkg
func green(in string) string {
	return fmt.Sprintf("\033[0;32m%s\033[0;0m", in)
}

func create(p string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0770); err != nil {
		return nil, err
	}
	return os.Create(p)
}
