package logkeeper

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	otelTrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	runtimeTrace "runtime/trace"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/urfave/negroni"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
)

type pprofsvc struct {
	tracer       otelTrace.Tracer
	otelGrpcConn *grpc.ClientConn
	closers      []closerOp
}

func NewPProfSvc(tracer otelTrace.Tracer) *pprofsvc {
	return &pprofsvc{tracer: tracer}
}

// GetHandlerPprof returns a handler for pprof endpoints.
func (p *pprofsvc) GetHandlerPprof(ctx context.Context) http.Handler {
	router := mux.NewRouter()
	router.Use(NewLogger(ctx).Middleware)
	router.Use(otelmux.Middleware("logkeeper"))

	root := router.PathPrefix("/debug/pprof").Subrouter()
	root.HandleFunc("/", p.index)
	root.HandleFunc("/heap", p.index)
	root.HandleFunc("/block", p.index)
	root.HandleFunc("/goroutine", p.index)
	root.HandleFunc("/mutex", p.index)
	root.HandleFunc("/threadcreate", p.index)
	root.HandleFunc("/cmdline", p.cmdline)
	root.HandleFunc("/profile", p.profile)
	root.HandleFunc("/symbol", p.symbol)
	root.HandleFunc("/trace", p.trace)

	n := negroni.New()
	n.UseHandler(router)
	return n
}

// ******************************************************************************
// The below was copied from the standard library net/http/pprof because we want
// to use our own router. This is identical with the exception of the init
// function (which registered handlers), which has been deleted.
// ******************************************************************************

// cmdline responds with the running program's
// command line, with arguments separated by NUL bytes.
// The package initialization registers it as /debug/pprof/cmdline.
func (p *pprofsvc) cmdline(w http.ResponseWriter, r *http.Request) {
	_, span := p.tracer.Start(r.Context(), "index")
	defer span.End()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, strings.Join(os.Args, "\x00"))
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// profile responds with the pprof-formatted cpu profile.
// The package initialization registers it as /debug/pprof/profile.
func (p *pprofsvc) profile(w http.ResponseWriter, r *http.Request) {
	ctx, span := p.tracer.Start(r.Context(), "index")
	defer span.End()
	sec, _ := strconv.ParseInt(r.FormValue("seconds"), 10, 64)
	if sec == 0 {
		sec = 30
	}

	// Set Content Type assuming StartCPUProfile will work,
	// because if it does it starts writing.
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := pprof.StartCPUProfile(w); err != nil {
		// StartCPUProfile failed, so no writes yet.
		// Can change header back to text content
		// and send error code.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Could not enable CPU profiling: %s\n", err)
		return
	}
	sleep(ctx, time.Duration(sec)*time.Second)
	pprof.StopCPUProfile()
}

// trace responds with the execution trace in binary form.
// Tracing lasts for duration specified in seconds GET parameter, or for 1 second if not specified.
// The package initialization registers it as /debug/pprof/trace.
func (p *pprofsvc) trace(w http.ResponseWriter, r *http.Request) {
	ctx, span := p.tracer.Start(r.Context(), "index")
	defer span.End()
	sec, err := strconv.ParseFloat(r.FormValue("seconds"), 64)
	if sec <= 0 || err != nil {
		sec = 1
	}

	// Set Content Type assuming trace.Start will work,
	// because if it does it starts writing.
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := runtimeTrace.Start(w); err != nil {
		// runtimeTrace.Start failed, so no writes yet.
		// Can change header back to text content and send error code.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "Could not enable tracing: %s\n", err)
		return
	}
	sleep(ctx, time.Duration(sec*float64(time.Second)))
	runtimeTrace.Stop()
}

// symbol looks up the program counters listed in the request,
// responding with a table mapping program counters to function names.
// The package initialization registers it as /debug/pprof/symbol.
func (p *pprofsvc) symbol(w http.ResponseWriter, r *http.Request) {
	_, span := p.tracer.Start(r.Context(), "index")
	defer span.End()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// We have to read the whole POST body before
	// writing any output. Buffer the output here.
	var buf bytes.Buffer

	// We don't know how many symbols we have, but we
	// do have symbol information. Pprof only cares whether
	// this number is 0 (no symbols available) or > 0.
	fmt.Fprintf(&buf, "num_symbols: 1\n")

	var b *bufio.Reader
	if r.Method == "POST" {
		b = bufio.NewReader(r.Body)
	} else {
		b = bufio.NewReader(strings.NewReader(r.URL.RawQuery))
	}

	for {
		word, err := b.ReadSlice('+')
		if err == nil {
			word = word[0 : len(word)-1] // trim +
		}
		pc, _ := strconv.ParseUint(string(word), 0, 64)
		if pc != 0 {
			f := runtime.FuncForPC(uintptr(pc))
			if f != nil {
				fmt.Fprintf(&buf, "%#x %s\n", pc, f.Name())
			}
		}

		// Wait until here to check for err; the last
		// symbol will have an err because it doesn't end in +.
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(&buf, "reading request: %v\n", err)
			}
			break
		}
	}

	_, _ = w.Write(buf.Bytes())
}

type pprofHandler string

func (name pprofHandler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	debug, _ := strconv.Atoi(r.FormValue("debug"))
	p := pprof.Lookup(string(name))
	if p == nil {
		w.WriteHeader(404)
		fmt.Fprintf(w, "Unknown profile: %s\n", name)
		return
	}
	gc, _ := strconv.Atoi(r.FormValue("gc"))
	if name == "heap" && gc > 0 {
		runtime.GC()
	}
	_ = p.WriteTo(w, debug)
}

// index responds with the pprof-formatted profile named by the request.
// For example, "/debug/pprof/heap" serves the "heap" profile.
// Index responds to a request for "/debug/pprof/" with an HTML page
// listing the available profiles.
func (p *pprofsvc) index(w http.ResponseWriter, r *http.Request) {
	_, span := p.tracer.Start(r.Context(), "index")
	defer span.End()
	if strings.HasPrefix(r.URL.Path, "/debug/pprof/") {
		name := strings.TrimPrefix(r.URL.Path, "/debug/pprof/")
		if name != "" {
			pprofHandler(name).serveHTTP(w, r)
			return
		}
	}

	profiles := pprof.Profiles()
	if err := indexTmpl.Execute(w, profiles); err != nil {
		log.Print(err)
	}
}

var indexTmpl = template.Must(template.New("index").Parse(`<html>
<head>
<title>/debug/pprof/</title>
</head>
<body>
/debug/pprof/<br>
<br>
profiles:<br>
<table>
{{range .}}
<tr><td align=right>{{.Count}}<td><a href="{{.Name}}?debug=1">{{.Name}}</a>
{{end}}
</table>
<br>
<a href="goroutine?debug=2">full goroutine stack dump</a><br>
</body>
</html>
`))
