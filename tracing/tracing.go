package tracing

import (
	"io"
	"io/ioutil"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"

	"github.com/uber/jaeger-client-go"
	"github.com/uber/jaeger-client-go/config"
	"github.com/uber/jaeger-client-go/log"
	"github.com/uber/jaeger-lib/metrics"
)

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
		// 设置最大 Tag 长度，根据情况设置
		config.MaxTagValueLength(65535),
	)
	if err != nil {
		panic(err)
	}

	return tracer, closer, err
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
		defer serverSpan.Finish()

		ext.HTTPUrl.Set(serverSpan, c.Request.URL.Path)
		ext.HTTPMethod.Set(serverSpan, c.Request.Method)
		ext.Component.Set(serverSpan, "Gin-Http")
		opentracing.Tag{Key: "http.headers.x-forwarded-for", Value: c.Request.Header.Get("X-Forwarded-For")}.Set(serverSpan)
		opentracing.Tag{Key: "http.headers.user-agent", Value: c.Request.Header.Get("User-Agent")}.Set(serverSpan)
		opentracing.Tag{Key: "request.time", Value: time.Now().Format(time.RFC3339)}.Set(serverSpan)
		opentracing.Tag{Key: "http.server.mode", Value: gin.Mode()}.Set(serverSpan)

		// TODO 不记录body
		body, err := ioutil.ReadAll(c.Request.Body)
		if err == nil {
			opentracing.Tag{Key: "http.request_body", Value: string(body)}.Set(serverSpan)
		}

		c.Request = c.Request.WithContext(opentracing.ContextWithSpan(c.Request.Context(), serverSpan))

		c.Next()
		if gin.Mode() == gin.DebugMode {
			opentracing.Tag{Key: "debug.trace", Value: string(debug.Stack())}.Set(serverSpan)
		}

		ext.HTTPStatusCode.Set(serverSpan, uint16(c.Writer.Status()))
		opentracing.Tag{Key: "request.errors", Value: c.Errors.String()}.Set(serverSpan)
	}
}
