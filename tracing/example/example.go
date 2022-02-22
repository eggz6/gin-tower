package main

import (
	"github.com/eggz6/gin-tower/tracing"
	"github.com/gin-gonic/gin"
	"github.com/opentracing/opentracing-go"
)

func main() {
	t, closer, err := tracing.NewGlobalTracer("example")
	if err != nil {
		panic(err)
	}
	defer closer.Close()
	opentracing.SetGlobalTracer(t)
	r := gin.Default()
	// 注入先前编写的中间件
	r.Use(tracing.OpenTracing())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})
	r.Run()

}
