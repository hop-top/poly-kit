package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// OpenAPIConfig holds metadata for the generated OpenAPI spec.
type OpenAPIConfig struct {
	Title       string
	Version     string
	Description string
	Servers     []ServerConfig
}

// ServerConfig describes a server entry in the OpenAPI spec.
type ServerConfig struct {
	URL         string
	Description string
}

// WithOpenAPI enables OpenAPI spec generation on the Router.
// When enabled, /openapi.json serves the generated spec.
func WithOpenAPI(cfg OpenAPIConfig) RouterOption {
	return func(r *Router) {
		humaCfg := huma.DefaultConfig(cfg.Title, cfg.Version)
		if cfg.Description != "" {
			humaCfg.Info.Description = cfg.Description
		}
		for _, s := range cfg.Servers {
			humaCfg.Servers = append(humaCfg.Servers, &huma.Server{
				URL:         s.URL,
				Description: s.Description,
			})
		}
		r.humaAPI = humago.New(r.mux, humaCfg)
	}
}

// HumaAPI returns the huma.API instance if OpenAPI is enabled, nil
// otherwise. Pass the result to WithHumaAPI to wire ResourceRouter
// operations into the spec.
func HumaAPI(r *Router) huma.API {
	return r.humaAPI
}

// WithHumaAPI tells ResourceRouter to register typed huma operations
// in addition to raw HandleFunc calls, enabling OpenAPI spec
// generation. mountPrefix is the path where the resource handler
// will be mounted (e.g. "/api"); it is combined with the resource
// prefix to form complete operation paths in the spec.
func WithHumaAPI[T Entity](api huma.API, mountPrefix ...string) ResourceOption[T] {
	return func(c *resourceConfig[T]) {
		if api == nil {
			return
		}
		c.humaAPI = api
		if len(mountPrefix) > 0 {
			c.humaMountPrefix = mountPrefix[0]
		}
	}
}

// humaCreateInput wraps a request body for the create operation.
type humaCreateInput[T Entity] struct {
	Body T
}

// humaCreateOutput wraps a response body for the create operation.
type humaCreateOutput[T Entity] struct {
	Body T
}

// humaGetInput captures the path ID for get/delete operations.
type humaGetInput struct {
	ID string `path:"id"`
}

// humaEntityOutput wraps a single entity response.
type humaEntityOutput[T Entity] struct {
	Body T
}

// humaListInput captures query parameters for the list operation.
type humaListInput struct {
	Limit  int    `query:"limit" default:"20" doc:"Max results"`
	Offset int    `query:"offset" default:"0" doc:"Skip N results"`
	Sort   string `query:"sort,omitempty" doc:"Sort field"`
	Search string `query:"search,omitempty" doc:"Search filter"`
}

// humaListOutput wraps a list response.
type humaListOutput[T Entity] struct {
	Body []T
}

// humaUpdateInput captures path ID and request body for update.
type humaUpdateInput[T Entity] struct {
	ID   string `path:"id"`
	Body T
}

// humaDeleteInput captures the path ID for delete.
type humaDeleteInput struct {
	ID string `path:"id"`
}

// humaDeleteOutput is an empty response for 204.
type humaDeleteOutput struct{}

// registerHumaOps registers typed huma operations for CRUD.
func registerHumaOps[T Entity](
	api huma.API,
	svc Service[T],
	cfg *resourceConfig[T],
	basePath string,
) {
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}

	enabled := func(name string) bool {
		if cfg.routes == nil {
			return true
		}
		return cfg.routes[name]
	}

	if enabled("create") {
		huma.Register(api, huma.Operation{
			OperationID:      operationID(basePath, "create"),
			Method:           http.MethodPost,
			Path:             basePath,
			Summary:          "Create resource",
			DefaultStatus:    http.StatusCreated,
			SkipValidateBody: true,
		}, func(ctx context.Context, input *humaCreateInput[T]) (*humaCreateOutput[T], error) {
			created, err := svc.Create(ctx, input.Body)
			if err != nil {
				return nil, mapHumaError(err)
			}
			return &humaCreateOutput[T]{Body: created}, nil
		})
	}

	if enabled("list") {
		huma.Register(api, huma.Operation{
			OperationID: operationID(basePath, "list"),
			Method:      http.MethodGet,
			Path:        basePath,
			Summary:     "List resources",
		}, func(ctx context.Context, input *humaListInput) (*humaListOutput[T], error) {
			q := Query{
				Limit:  input.Limit,
				Offset: input.Offset,
				Sort:   input.Sort,
				Search: input.Search,
			}
			items, err := svc.List(ctx, q)
			if err != nil {
				return nil, mapHumaError(err)
			}
			return &humaListOutput[T]{Body: items}, nil
		})
	}

	if enabled("get") {
		huma.Register(api, huma.Operation{
			OperationID: operationID(basePath, "get"),
			Method:      http.MethodGet,
			Path:        basePath + "{id}",
			Summary:     "Get resource by ID",
		}, func(ctx context.Context, input *humaGetInput) (*humaEntityOutput[T], error) {
			item, err := svc.Get(ctx, input.ID)
			if err != nil {
				return nil, mapHumaError(err)
			}
			return &humaEntityOutput[T]{Body: item}, nil
		})
	}

	if enabled("update") {
		huma.Register(api, huma.Operation{
			OperationID: operationID(basePath, "update"),
			Method:      http.MethodPut,
			Path:        basePath + "{id}",
			Summary:     "Update resource",
		}, func(ctx context.Context, input *humaUpdateInput[T]) (*humaEntityOutput[T], error) {
			if input.Body.GetID() != input.ID {
				return nil, huma.Error400BadRequest("path id does not match body id")
			}
			updated, err := svc.Update(ctx, input.Body)
			if err != nil {
				return nil, mapHumaError(err)
			}
			return &humaEntityOutput[T]{Body: updated}, nil
		})
	}

	if enabled("delete") {
		huma.Register(api, huma.Operation{
			OperationID:   operationID(basePath, "delete"),
			Method:        http.MethodDelete,
			Path:          basePath + "{id}",
			Summary:       "Delete resource",
			DefaultStatus: http.StatusNoContent,
		}, func(ctx context.Context, input *humaDeleteInput) (*humaDeleteOutput, error) {
			if err := svc.Delete(ctx, input.ID); err != nil {
				return nil, mapHumaError(err)
			}
			return nil, nil
		})
	}
}

// operationID generates a stable operation ID from path + action.
func operationID(basePath, action string) string {
	// Strip leading/trailing slashes, replace remaining with dashes.
	clean := basePath
	for len(clean) > 0 && clean[0] == '/' {
		clean = clean[1:]
	}
	for len(clean) > 0 && clean[len(clean)-1] == '/' {
		clean = clean[:len(clean)-1]
	}
	if clean == "" {
		return action
	}
	// Replace slashes with dashes.
	result := make([]byte, 0, len(clean)+1+len(action))
	for i := range len(clean) {
		if clean[i] == '/' {
			result = append(result, '-')
		} else {
			result = append(result, clean[i])
		}
	}
	result = append(result, '-')
	result = append(result, action...)
	return string(result)
}

// mapHumaError converts domain errors to huma errors.
func mapHumaError(err error) error {
	ae := MapError(err)
	return huma.NewError(ae.Status, ae.Message)
}
