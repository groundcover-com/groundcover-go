module github.com/groundcover-com/groundcover-go/prometheus

go 1.25

require (
	github.com/VictoriaMetrics/metrics v1.44.0
	github.com/groundcover-com/groundcover-go v0.2.0
)

require (
	github.com/valyala/fastrand v1.1.0 // indirect
	github.com/valyala/histogram v1.2.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
)

replace github.com/groundcover-com/groundcover-go => ../
