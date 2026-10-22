package api

import (
	"errors"
	"fmt"
	"slices"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api/apidoc"
	"example.com/goservice/internal/api/middleware"
)

// Spec renders the OpenAPI document of routes: the content of
// docs/openapi.json. It mounts every registrar on a fresh engine, under the
// prefix NewRouter mounts it at, and fails when a mounted route has no docs or
// a documented route is not mounted, so the docs describe exactly what the
// service serves. Registrars are only mounted, never called: handlers built
// with no dependencies are enough.
func Spec(info apidoc.Info, routes Routes) ([]byte, error) {
	for _, h := range middleware.SecurityHeaders() {
		info.ResponseHeaders = append(info.ResponseHeaders, apidoc.Param{
			Name:        h[0],
			Description: "Set on every response.",
			Required:    true,
			Enum:        []string{h[1]},
			Example:     h[1],
		})
	}
	var (
		mounts []apidoc.Mount
		errs   []error
	)
	for _, surface := range []struct {
		prefix string
		regs   []Registrar
	}{{"", routes.Probes}, {"/v1", routes.V1}} {
		for _, reg := range surface.regs {
			m, documented, err := mount(surface.prefix, reg)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if documented {
				mounts = append(mounts, m)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return apidoc.Build(info, mounts)
}

// mount registers reg on a fresh engine and checks its Docs against the
// routes it mounted. documented is false for a registrar that mounts nothing.
func mount(prefix string, reg Registrar) (m apidoc.Mount, documented bool, err error) {
	e := gin.New()
	var r gin.IRouter = e
	if prefix != "" {
		r = e.Group(prefix)
	}
	reg.Register(r)
	var mounted []string
	for _, rt := range e.Routes() {
		mounted = append(mounted, rt.Method+" "+rt.Path)
	}

	d, ok := reg.(apidoc.Documented)
	if !ok {
		if len(mounted) == 0 {
			return apidoc.Mount{}, false, nil
		}
		return apidoc.Mount{}, false, fmt.Errorf("%T mounts %v but has no Docs method", reg, mounted)
	}
	group := d.Docs()
	var docs []string
	for _, op := range group.Ops {
		route := op.Route
		if route == "" {
			route = op.Path
		}
		docs = append(docs, op.Method+" "+prefix+route)
	}
	var errs []error
	for _, rt := range mounted {
		if !slices.Contains(docs, rt) {
			errs = append(errs, fmt.Errorf("%T: %s is mounted but not documented in Docs", reg, rt))
		}
	}
	for _, rt := range docs {
		if !slices.Contains(mounted, rt) {
			errs = append(errs, fmt.Errorf("%T: %s is documented but not mounted by Register", reg, rt))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return apidoc.Mount{}, false, err
	}
	return apidoc.Mount{Prefix: prefix, Group: group}, true, nil
}
