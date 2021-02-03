package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/carelinus/ssl-proxy/gen"
	"github.com/carelinus/ssl-proxy/reverseproxy"
	"golang.org/x/crypto/acme/autocert"
)

var (
	to           = flag.String("to", "http://127.0.0.1:80", "the address and port for which to proxy requests to")
	fromURL      = flag.String("from", "127.0.0.1:4430", "the tcp address and port this proxy should listen for requests on")
	certFile     = flag.String("cert", "", "path to a tls certificate file. If not provided, ssl-proxy will generate one for you in ~/.ssl-proxy/")
	keyFile      = flag.String("key", "", "path to a private key file. If not provided, ssl-proxy will generate one for you in ~/.ssl-proxy/")
	domain       = flag.String("domain", "", "domain to mint letsencrypt certificates for. Usage of this parameter implies acceptance of the LetsEncrypt terms of service.")
	redirectHTTP = flag.Bool("redirectHTTP", false, "if true, redirects http requests from port 80 to https at your fromURL")
	altnames     = flag.String("altnames", "localhost", "comma separated altnames for the certificate DNS field")
)

const (
	DefaultCertFile = "cert.pem"
	DefaultKeyFile  = "key.pem"
	HTTPSPrefix     = "https://"
	HTTPPrefix      = "http://"
)

func main() {
	flag.Parse()

	validCertFile := *certFile != ""
	validKeyFile := *keyFile != ""
	validDomain := *domain != ""

	// Determine if we need to generate self-signed certs
	if (!validCertFile || !validKeyFile) && !validDomain {
		// Use default file paths
		*certFile = DefaultCertFile
		*keyFile = DefaultKeyFile

		log.Printf("No existing cert or key specified, generating some self-signed certs for use (%s, %s)\n", *certFile, *keyFile)

		// Generate new keys
		certBuf, keyBuf, fingerprint, err := gen.Keys(365*24*time.Hour, strings.Split(*altnames, ","))
		if err != nil {
			log.Fatal("Error generating default keys", err)
		}

		certOut, err := os.Create(*certFile)
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
	if *redirectHTTP {
		// Redirect to fromURL by default, unless a domain is specified--in that case, redirect using the public facing
		// domain
		redirectURL := *fromURL
		if validDomain {
			redirectURL = *domain
		}
		redirectTLS := func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+redirectURL+r.RequestURI, http.StatusMovedPermanently)
		}
		go func() {
			log.Println(
				fmt.Sprintf("Also redirecting https requests on port 80 to https requests on %s", redirectURL))
			err := http.ListenAndServe(":80", http.HandlerFunc(redirectTLS))
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
