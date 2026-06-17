package main

import "net/http"

type DockerProxy struct{}

func NewDockerProxy(cfg *ProxyConfig) (*DockerProxy, error) {
	return &DockerProxy{}, nil
}

func (dp *DockerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (dp *DockerProxy) Cleanup() {}
