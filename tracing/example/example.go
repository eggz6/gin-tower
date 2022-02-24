package main

import (
	"context"
	"log"
	"net/http"

	"github.com/eggz6/gin-tower/tracing"
	"github.com/gin-gonic/gin"
	opentracing "github.com/opentracing/opentracing-go"
)

func main() {
	openTracing, closer, err := tracing.Open("example")
	if err != nil {
		log.Fatalf("open tracing failed. err=%v", err)
	}

	defer closer.Close()

	r := gin.Default()
	r.Use(openTracing)
	r.GET("/ping", func(c *gin.Context) {
		call(c.Request.Context())

		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	r.GET("/hello", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "world",
		})
	})

	r.Run()
}

func call(ctx context.Context) error {
	req, err := http.NewRequest("GET", "http://localhost:8080/hello", nil)
	if err != nil {
		return err
	}

	tracer := opentracing.GlobalTracer()

	req = tracing.ContextToHTTP(ctx, tracer, req)

	cli := http.Client{}
	cli.Do(req)

	return nil
}
