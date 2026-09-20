package main

import (
	"github.com/yy1105384898/sub2api-openai-subscription-monitor/internal/monitor"
	pluginv1 "github.com/yy1105384898/sub2api-openai-subscription-monitor/pluginapi/v1"
)

var version = "0.2.0"

func main() {
	pluginv1.Serve(monitor.New(version))
}
