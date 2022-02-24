package tracing

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"

	jaeger "github.com/uber/jaeger-client-go"
	"github.com/uber/jaeger-client-go/config"
	"github.com/uber/jaeger-client-go/log"
	"github.com/uber/jaeger-lib/metrics"
)

var openOnce sync.Once

func NewGlobalTracer(name string) (opentracing.Tracer, io.Closer, error) {
	cfg := config.Configuration{
		ServiceName: name,
		Sampler: &config.SamplerConfig{
			Type:  jaeger.SamplerTypeConst,
			Param: 1,
		},
		Reporter: &config.ReporterConfig{
			LogSpans: true,
		},
	}

	jLogger := log.StdLogger
	jMetricsFactory := metrics.NullFactory

	tracer, closer, err := cfg.NewTracer(
		config.Logger(jLogger),
		config.Metrics(jMetricsFactory),
		config.MaxTagValueLength(65535),
	)
	if err != nil {
		panic(err)
	}

	return tracer, closer, err
}

func Open(name string) (gin.HandlerFunc, io.Closer, error) {
	var closer io.Closer
	var err error

	openOnce.Do(func() {
		t, c, e := NewGlobalTracer(name)
		if e != nil {
			err = e
			return
		}

		closer = c
		opentracing.SetGlobalTracer(t)
	})

	if err != nil {
		return nil, nil, err
	}

	return OpenTracing(), closer, nil
}

func OpenTracing() gin.HandlerFunc {
	return func(c *gin.Context) {
		wireCtx, _ := opentracing.GlobalTracer().Extract(
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(c.Request.Header),
		)

		serverSpan := opentracing.StartSpan(
			c.Request.URL.Path,
			ext.RPCServerOption(wireCtx),
		)

		spCtx, ok := serverSpan.Context().(*jaeger.SpanContext)
		var traceID string
		if ok {
			traceID = spCtx.TraceID().String()
			c.Set("Trace-ID", traceID)
		}

		defer serverSpan.Finish()

		ext.HTTPUrl.Set(serverSpan, c.Request.URL.Path)
		ext.HTTPMethod.Set(serverSpan, c.Request.Method)
		ip, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		ext.PeerHostIPv4.SetString(serverSpan, ip)
		ext.Component.Set(serverSpan, "Gin-Http")
		opentracing.Tag{Key: "http.headers.x-forwarded-for", Value: c.Request.Header.Get("X-Forwarded-For")}.Set(serverSpan)
		opentracing.Tag{Key: "http.headers.user-agent", Value: c.Request.Header.Get("User-Agent")}.Set(serverSpan)
		opentracing.Tag{Key: "request.time", Value: time.Now().Format(time.RFC3339)}.Set(serverSpan)
		opentracing.Tag{Key: "http.server.mode", Value: gin.Mode()}.Set(serverSpan)

		reqCtx := context.WithValue(c.Request.Context(), "Trace-ID", traceID)

		c.Request = c.Request.WithContext(opentracing.ContextWithSpan(reqCtx, serverSpan))

		c.Next()
		if gin.Mode() == gin.DebugMode {
			opentracing.Tag{Key: "debug.trace", Value: string(debug.Stack())}.Set(serverSpan)
		}

		ext.HTTPStatusCode.Set(serverSpan, uint16(c.Writer.Status()))
		opentracing.Tag{Key: "request.errors", Value: c.Errors.String()}.Set(serverSpan)
	}
}

// ContextToHTTP returns an http RequestFunc that injects an OpenTracing Span
// found in `ctx` into the http headers. If no such Span can be found, the
// RequestFunc is a noop.
func ContextToHTTP(ctx context.Context, tracer opentracing.Tracer, req *http.Request) (nReq *http.Request) {
	// Try to find a Span in the Context.
	span := opentracing.SpanFromContext(ctx)
	if span == nil {
		return req
	}

	span = opentracing.StartSpan(req.URL.Path,
		opentracing.ChildOf(span.Context()),
		opentracing.Tag{Key: string(ext.Component), Value: "HTTP"},
		ext.SpanKindRPCClient,
	)
	defer span.Finish()

	// http trace
	r := &requestTracer{sp: span}
	nCtx := httptrace.WithClientTrace(ctx, r.clientTrace())
	nReq = req.WithContext(nCtx)

	// Add standard OpenTracing tags.
	ext.HTTPMethod.Set(span, nReq.Method)
	ext.HTTPUrl.Set(span, nReq.URL.String())
	host, portString, err := net.SplitHostPort(nReq.URL.Host)
	if err == nil {
		ext.PeerHostname.Set(span, host)
		if port, err := strconv.Atoi(portString); err != nil {
			ext.PeerPort.Set(span, uint16(port))
		}
	} else {
		ext.PeerHostname.Set(span, nReq.URL.Host)
	}

	// There's nothing we can do with any errors here.
	tracer.Inject(span.Context(), opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(nReq.Header))
	return nReq
}

// HTTPToContext returns an http RequestFunc that tries to join with an
// OpenTracing trace found in `req` and starts a new Span called
// `operationName` accordingly. If no trace could be found in `req`, the Span
// will be a trace root. The Span is incorporated in the returned Context and
// can be retrieved with opentracing.SpanFromContext(ctx).
func HTTPToContext(tracer opentracing.Tracer, req *http.Request, operationName string) context.Context {
	// Try to join to a trace propagated in `req`.
	var span opentracing.Span
	wireContext, _ := tracer.Extract(opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(req.Header))
	span = tracer.StartSpan(operationName, ext.RPCServerOption(wireContext))
	ext.HTTPMethod.Set(span, req.Method)
	ext.HTTPUrl.Set(span, req.URL.String())
	ip, _, _ := net.SplitHostPort(req.RemoteAddr)
	ext.PeerHostIPv4.SetString(span, ip)
	return opentracing.ContextWithSpan(context.Background(), span)
}

func ExtractSpanFromCtx(ctx context.Context) opentracing.Span {
	return opentracing.SpanFromContext(ctx)
}

func ExtractTraceIDFromCtx(ctx context.Context) string {
	span := ExtractSpanFromCtx(ctx)

	spCtx, ok := span.Context().(*jaeger.SpanContext)
	if !ok {
		return ""
	}

	return spCtx.String()
}
