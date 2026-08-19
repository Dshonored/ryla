package router

import (
	"fmt"
	"net/url"
	"sort"
	"sync"
)

// registry holds named routes. It is shared by a root router and every
// sub-router derived from it, so names resolve across the whole application.
type registry struct {
	mu     sync.RWMutex
	routes map[string]NamedRoute
}

func newRegistry() *registry {
	return &registry{routes: make(map[string]NamedRoute)}
}

func (reg *registry) add(name, method, pattern string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	if existing, ok := reg.routes[name]; ok {
		panic(fmt.Sprintf(
			"router: duplicate route name %q (%s %s and %s %s)",
			name, existing.Method, existing.Pattern, method, pattern,
		))
	}
	reg.routes[name] = NamedRoute{Name: name, Method: method, Pattern: pattern}
}

func (reg *registry) build(name string, params ...any) (string, error) {
	reg.mu.RLock()
	rt, ok := reg.routes[name]
	reg.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("router: no route named %q", name)
	}
	if len(params)%2 != 0 {
		return "", fmt.Errorf("router: route %q: params must be key/value pairs, got %d", name, len(params))
	}

	values := make(map[string]string, len(params)/2)
	for i := 0; i < len(params); i += 2 {
		key, ok := params[i].(string)
		if !ok {
			return "", fmt.Errorf("router: route %q: param name at index %d is not a string", name, i)
		}
		values[key] = fmt.Sprint(params[i+1])
	}

	path, err := fillPattern(rt.Pattern, values)
	if err != nil {
		return "", err
	}

	// Anything left over becomes a query string. This makes
	// URL("users.index", "page", 2) produce /users?page=2 without a second API.
	if len(values) > 0 {
		q := make(url.Values, len(values))
		for k, v := range values {
			q.Set(k, v)
		}
		path += "?" + q.Encode()
	}
	return path, nil
}

func (reg *registry) all() []NamedRoute {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	out := make([]NamedRoute, 0, len(reg.routes))
	for _, rt := range reg.routes {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
