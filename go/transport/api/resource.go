package api

import (
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// ResourceOption configures a ResourceRouter.
type ResourceOption[T Entity] func(*resourceConfig[T])

// Serializer provides custom JSON encode/decode for a resource.
type Serializer[T Entity] struct {
	Encode func(w http.ResponseWriter, status int, v T)
	Decode func(r *http.Request) (T, error)
}

// QueryParser converts query string parameters into a Query.
type QueryParser func(r *http.Request) Query

type resourceConfig[T Entity] struct {
	prefix          string
	serializer      *Serializer[T]
	queryParser     QueryParser
	routes          map[string]bool // nil = all routes
	humaAPI         huma.API
	humaMountPrefix string
}

// WithPrefix sets a URL prefix for the resource routes.
func WithPrefix[T Entity](prefix string) ResourceOption[T] {
	return func(c *resourceConfig[T]) {
		c.prefix = prefix
	}
}

// WithSerializer sets custom encode/decode functions.
func WithSerializer[T Entity](s Serializer[T]) ResourceOption[T] {
	return func(c *resourceConfig[T]) {
		c.serializer = &s
	}
}

// WithQueryParser sets a custom query string parser.
func WithQueryParser[T Entity](p QueryParser) ResourceOption[T] {
	return func(c *resourceConfig[T]) {
		c.queryParser = p
	}
}

// WithRouteFilter limits which routes are registered.
// Valid names: "create", "list", "get", "update", "delete".
func WithRouteFilter[T Entity](routes ...string) ResourceOption[T] {
	return func(c *resourceConfig[T]) {
		c.routes = make(map[string]bool, len(routes))
		for _, r := range routes {
			c.routes[r] = true
		}
	}
}

// ResourceRouter returns an http.Handler that provides standard CRUD
// endpoints for an entity type T backed by the given Service.
//
// Routes:
//
//	POST   {prefix}/     → Create
//	GET    {prefix}/     → List
//	GET    {prefix}/{id} → Get
//	PUT    {prefix}/{id} → Update
//	DELETE {prefix}/{id} → Delete
func ResourceRouter[T Entity](svc Service[T], opts ...ResourceOption[T]) http.Handler {
	cfg := &resourceConfig[T]{
		queryParser: DefaultQueryParser,
	}
	for _, o := range opts {
		o(cfg)
	}

	mux := http.NewServeMux()
	base := cfg.prefix + "/"

	enabled := func(name string) bool {
		if cfg.routes == nil {
			return true
		}
		return cfg.routes[name]
	}

	encode := func(w http.ResponseWriter, status int, v T) {
		if cfg.serializer != nil && cfg.serializer.Encode != nil {
			cfg.serializer.Encode(w, status, v)
			return
		}
		JSON(w, status, v)
	}

	decode := func(r *http.Request) (T, error) {
		if cfg.serializer != nil && cfg.serializer.Decode != nil {
			return cfg.serializer.Decode(r)
		}
		return Bind[T](r)
	}

	if enabled("create") {
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
			entity, err := decode(r)
			if err != nil {
				Error(w, http.StatusBadRequest, MapError(err))
				return
			}
			created, err := svc.Create(r.Context(), entity)
			if err != nil {
				ae := MapError(err)
				Error(w, ae.Status, ae)
				return
			}
			encode(w, http.StatusCreated, created)
		})
	}

	if enabled("list") {
		mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
			q := cfg.queryParser(r)
			items, err := svc.List(r.Context(), q)
			if err != nil {
				ae := MapError(err)
				Error(w, ae.Status, ae)
				return
			}
			JSON(w, http.StatusOK, items)
		})
	}

	if enabled("get") {
		mux.HandleFunc("GET "+base+"{id}", func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			item, err := svc.Get(r.Context(), id)
			if err != nil {
				ae := MapError(err)
				Error(w, ae.Status, ae)
				return
			}
			encode(w, http.StatusOK, item)
		})
	}

	if enabled("update") {
		mux.HandleFunc("PUT "+base+"{id}", func(w http.ResponseWriter, r *http.Request) {
			pathID := r.PathValue("id")
			entity, err := decode(r)
			if err != nil {
				Error(w, http.StatusBadRequest, MapError(err))
				return
			}
			if entity.GetID() != pathID {
				Error(w, http.StatusBadRequest, &APIError{
					Status:  http.StatusBadRequest,
					Code:    "id_mismatch",
					Message: "path id does not match body id",
				})
				return
			}
			updated, err := svc.Update(r.Context(), entity)
			if err != nil {
				ae := MapError(err)
				Error(w, ae.Status, ae)
				return
			}
			encode(w, http.StatusOK, updated)
		})
	}

	if enabled("delete") {
		mux.HandleFunc("DELETE "+base+"{id}", func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			if err := svc.Delete(r.Context(), id); err != nil {
				ae := MapError(err)
				Error(w, ae.Status, ae)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	// When huma is enabled, register typed operations for OpenAPI spec.
	if cfg.humaAPI != nil {
		basePath := cfg.humaMountPrefix + cfg.prefix + "/"
		registerHumaOps(cfg.humaAPI, svc, cfg, basePath)
	}

	return mux
}

// DefaultQueryParser maps standard query parameters to a Query struct.
//
//	limit  → Query.Limit  (default 20)
//	offset → Query.Offset (default 0)
//	sort   → Query.Sort
//	search → Query.Search
func DefaultQueryParser(r *http.Request) Query {
	q := Query{Limit: 20}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			q.Offset = n
		}
	}
	q.Sort = r.URL.Query().Get("sort")
	q.Search = r.URL.Query().Get("search")
	return q
}
