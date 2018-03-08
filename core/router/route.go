package router

import (
	"fmt"
	"strings"

	"github.com/kataras/iris/context"
	"github.com/kataras/iris/core/router/macro"
)

// Route contains the information about a registered Route.
// If any of the following fields are changed then the
// caller should Refresh the router.
// There are special routes which are used for fallback mechanism:
// - Party Route: which is used to store fallback handlers executed when no route is found in a Party,
// - Global Fallback Route: which is used to store fallback handlers executed when no route is found in application.
// If no route found in all Parties, so Global Fallback Route is used as fallback route.
// Global Fallback Route is not stored with others routes but is stored in a special field in internal structures.
type Route struct {
	Name      string          // "userRoute"
	Method    string          // "GET"
	Subdomain string          // "admin."
	tmpl      *macro.Template // Tmpl().Src: "/api/user/{id:int}"
	Path      string          // "/api/user/{id}"
	// temp storage, they're appended to the Handlers on build.
	// Execution happens before Handlers, can be empty.
	beginHandlers context.Handlers
	// Handlers are the main route's handlers, executed by order.
	// Cannot be empty.
	Handlers        context.Handlers
	MainHandlerName string
	// temp storage, they're appended to the Handlers on build.
	// Execution happens after Begin and main Handler(s), can be empty.
	doneHandlers context.Handlers
	// FormattedPath all dynamic named parameters (if any) replaced with %v,
	// used by Application to validate param values of a Route based on its name.
	FormattedPath string

	// beginHandlerIndex keep tracking of the position to add new fallback handlers.
	// Even if Begin Handlers is add in beginHandlers, new begin handlers can be added after this route is built.
	// Therefore, without this index, new begin handler, would be prepended to all handlers.
	beginHandlerIndex int
	// fallbackHandlerIndex keep tracking of the position to add new fallback handlers.
	// Fallback handler is normal handlers (neither a begin nor a done handler) for a special route.
	// But new fallback handlers can be added after this route is built.
	fallbackHandlerIndex int

	// If true, so the route is a special route (Party Route or the Global Fallback Route).
	// If false, so the node represents a normal route.
	// Special route will contain middlewares in handlers which will be called before fallback handlers.
	isSpecial bool
}

// NewRoute returns a new route based on its method,
// subdomain, the path (unparsed or original),
// handlers and the macro container which all routes should share.
// It parses the path based on the "macros",
// handlers are being changed to validate the macros at serve time, if needed.
func NewRoute(method, subdomain, unparsedPath, mainHandlerName string,
	handlers context.Handlers, macros *macro.Map) (*Route, error) {

	tmpl, err := macro.Parse(unparsedPath, macros)
	if err != nil {
		return nil, err
	}

	path, handlers, err := compileRoutePathAndHandlers(handlers, tmpl)
	if err != nil {
		return nil, err
	}

	path = cleanPath(path) // maybe unnecessary here but who cares in this moment
	defaultName := method + subdomain + tmpl.Src
	formattedPath := formatPath(path)

	route := &Route{
		Name:                 defaultName,
		Method:               method,
		Subdomain:            subdomain,
		tmpl:                 tmpl,
		Path:                 path,
		Handlers:             handlers,
		MainHandlerName:      mainHandlerName,
		FormattedPath:        formattedPath,
		fallbackHandlerIndex: len(handlers),
	}

	return route, nil
}

// special declare this route as a special route (a Party Route or the Global Fallback Route)
func (r *Route) special() *Route {
	r.isSpecial = true

	return r
}

// use adds explicit begin handlers(middleware) to this route,
// It's being called internally, it's useless for outsiders
// because `Handlers` field is exported.
// The callers of this function are: `APIBuilder#UseGlobal` and `APIBuilder#Done`.
//
// BuildHandlers should be called to build the route's `Handlers`.
func (r *Route) use(handlers context.Handlers) {
	if len(handlers) == 0 {
		return
	}
	r.beginHandlers = append(r.beginHandlers, handlers...)
}

// done adds explicit done handlers to this route.
// It's being called internally, it's useless for outsiders
// because `Handlers` field is exported.
// The callers of this function are: `APIBuilder#UseGlobal` and `APIBuilder#Done`.
//
// BuildHandlers should be called to build the route's `Handlers`.
func (r *Route) done(handlers context.Handlers) {
	if len(handlers) == 0 {
		return
	}
	r.doneHandlers = append(r.doneHandlers, handlers...)
}

// fallback adds explicit fallback handlers to this route.
// It's being called internally, it's useless for outsiders
// because `Handlers` field is exported.
// The only caller of this function are: `APIBuilder#Fallback` .
func (r *Route) fallback(handlers context.Handlers) {
	if (len(handlers) == 0) && (!r.isSpecial) {
		return
	}

	r.Handlers = append(r.Handlers, handlers...)
	if len(r.Handlers) != r.fallbackHandlerIndex {
		start := r.fallbackHandlerIndex
		r.fallbackHandlerIndex += len(handlers)

		copy(r.Handlers[r.fallbackHandlerIndex:], r.Handlers[start:])
		copy(r.Handlers[start:], handlers)
	}
}

// SetName set route name in a continious call style
func (r *Route) SetName(name string) *Route {
	r.Name = name

	return r
}

// BuildHandlers is executed automatically by the router handler
// at the `Application#Build` state. Do not call it manually, unless
// you were defined your own request mux handler.
func (r *Route) BuildHandlers() context.Handlers {
	beginHandlerCount := len(r.beginHandlers)
	if beginHandlerCount > 0 {
		// Update fallback handler index
		r.fallbackHandlerIndex += beginHandlerCount

		// Prepend new begin handlers
		r.Handlers = append(r.beginHandlers, r.Handlers...)

		// Start index for begin handlers before build
		start := r.beginHandlerIndex

		// Update begin handler index
		r.beginHandlerIndex += beginHandlerCount

		// If begin handler is in the middle (not at the head of handler list)
		if start > 0 {
			// So move old begin handler to the head of handler list
			copy(r.Handlers, r.Handlers[beginHandlerCount:r.beginHandlerIndex])

			// And move new begin handler to their right place
			copy(r.Handlers[start:], r.beginHandlers)
		}

		r.beginHandlers = r.beginHandlers[0:0]
	}

	if len(r.doneHandlers) > 0 {
		r.Handlers = append(r.Handlers, r.doneHandlers...)
		r.doneHandlers = r.doneHandlers[0:0]
	} // note: no mutex needed, this should be called in-sync when server is not running of course.

	if len(r.Handlers) == 0 {
		return nil
	}

	return r.Handlers
}

// String returns the form of METHOD, SUBDOMAIN, TMPL PATH.
func (r *Route) String() string {
	special := ""
	if r.isSpecial {
		special = " (*)"
	}

	return fmt.Sprintf("%s %s%s%s", r.Method, r.Subdomain, r.Tmpl().Src, special)
}

// Tmpl returns the path template, i
// it contains the parsed template
// for the route's path.
// May contain zero named parameters.
//
// Developer can get his registered path
// via Tmpl().Src, Route.Path is the path
// converted to match the underline router's specs.
func (r Route) Tmpl() macro.Template {
	return *r.tmpl
}

// IsOnline returns true if the route is marked as "online" (state).
func (r Route) IsOnline() bool {
	return r.Method != MethodNone
}

// IsSpecial returns:
// - true, so the route is a special route (Party Route or the Global Fallback Route).
// - false, so the node represents a normal route.
func (r Route) IsSpecial() bool {
	return r.isSpecial
}

// formats the parsed to the underline path syntax.
// path = "/api/users/:id"
// return "/api/users/%v"
//
// path = "/files/*file"
// return /files/%v
//
// path = "/:username/messages/:messageid"
// return "/%v/messages/%v"
// we don't care about performance here, it's prelisten.
func formatPath(path string) string {
	if strings.Contains(path, ParamStart) || strings.Contains(path, WildcardParamStart) {
		var (
			startRune         = ParamStart[0]
			wildcardStartRune = WildcardParamStart[0]
		)

		var formattedParts []string
		parts := strings.Split(path, "/")
		for _, part := range parts {
			if len(part) == 0 {
				continue
			}
			if part[0] == startRune || part[0] == wildcardStartRune {
				// is param or wildcard param
				part = "%v"
			}
			formattedParts = append(formattedParts, part)
		}

		return "/" + strings.Join(formattedParts, "/")
	}
	// the whole path is static just return it
	return path
}

// StaticPath returns the static part of the original, registered route path.
// if /user/{id} it will return /user
// if /user/{id}/friend/{friendid:int} it will return /user too
// if /assets/{filepath:path} it will return /assets.
func (r Route) StaticPath() string {
	src := r.tmpl.Src
	bidx := strings.IndexByte(src, '{')
	if bidx == -1 || len(src) <= bidx {
		return src // no dynamic part found
	}
	if bidx == 0 { // found at first index,
		// but never happens because of the prepended slash
		return "/"
	}

	return src[:bidx]
}

// ResolvePath returns the formatted path's %v replaced with the args.
func (r Route) ResolvePath(args ...string) string {
	rpath, formattedPath := r.Path, r.FormattedPath
	if rpath == formattedPath {
		// static, no need to pass args
		return rpath
	}
	// check if we have /*, if yes then join all arguments to one as path and pass that as parameter
	if rpath[len(rpath)-1] == WildcardParamStart[0] {
		parameter := strings.Join(args, "/")
		return fmt.Sprintf(formattedPath, parameter)
	}
	// else return the formattedPath with its args,
	// the order matters.
	for _, s := range args {
		formattedPath = strings.Replace(formattedPath, "%v", s, 1)
	}
	return formattedPath
}

// Trace returns some debug infos as a string sentence.
// Should be called after Build.
func (r Route) Trace() string {
	printfmt := fmt.Sprintf("%s:", r.Method)
	if r.Subdomain != "" {
		printfmt += fmt.Sprintf(" %s", r.Subdomain)
	}
	printfmt += fmt.Sprintf(" %s ", r.Tmpl().Src)
	if l := len(r.Handlers); l > 1 {
		printfmt += fmt.Sprintf("-> %s() and %d more", r.MainHandlerName, l-1)
	} else {
		printfmt += fmt.Sprintf("-> %s()", r.MainHandlerName)
	}

	// printfmt := fmt.Sprintf("%s: %s >> %s", r.Method, r.Subdomain+r.Tmpl().Src, r.MainHandlerName)
	// if l := len(r.Handlers); l > 0 {
	// 	printfmt += fmt.Sprintf(" and %d more", l)
	// }
	return printfmt // without new line.
}

type routeReadOnlyWrapper struct {
	*Route
}

func (rd routeReadOnlyWrapper) Method() string {
	return rd.Route.Method
}

func (rd routeReadOnlyWrapper) Name() string {
	return rd.Route.Name
}

func (rd routeReadOnlyWrapper) Subdomain() string {
	return rd.Route.Subdomain
}

func (rd routeReadOnlyWrapper) Path() string {
	return rd.Route.tmpl.Src
}

func (rd routeReadOnlyWrapper) Trace() string {
	return rd.Route.Trace()
}
