package mvc

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/kataras/iris/v12/context"
	"github.com/kataras/iris/v12/core/router"
	"github.com/kataras/iris/v12/hero"
	"github.com/kataras/iris/v12/macro"
)

// BaseController is the optional controller interface, if it's
// completed by the end controller then the BeginRequest and EndRequest
// are called between the controller's method responsible for the incoming request.
type BaseController interface {
	BeginRequest(context.Context)
	EndRequest(context.Context)
}

type shared interface {
	Name() string
	Router() router.Party
	GetRoute(methodName string) *router.Route
	GetRoutes(methodName string) []*router.Route
	Handle(httpMethod, path, funcName string, middleware ...context.Handler) *router.Route
	HandleMany(httpMethod, path, funcName string, middleware ...context.Handler) []*router.Route
}

// BeforeActivation is being used as the only one input argument of a
// `func(c *Controller) BeforeActivation(b mvc.BeforeActivation) {}`.
//
// It's being called before the controller's dependencies binding to the fields or the input arguments
// but before server ran.
//
// It's being used to customize a controller if needed inside the controller itself,
// it's called once per application.
type BeforeActivation interface {
	shared
	Dependencies() *hero.Container
}

// AfterActivation is being used as the only one input argument of a
// `func(c *Controller) AfterActivation(a mvc.AfterActivation) {}`.
//
// It's being called after the `BeforeActivation`,
// and after controller's dependencies bind-ed to the fields or the input arguments but before server ran.
//
// It's being used to customize a controller if needed inside the controller itself,
// it's called once per application.
type AfterActivation interface {
	shared
	Singleton() bool
	DependenciesReadOnly() []*hero.Dependency
}

var (
	_ BeforeActivation = (*ControllerActivator)(nil)
	_ AfterActivation  = (*ControllerActivator)(nil)
)

// ControllerActivator returns a new controller type info description.
// Its functionality can be overridden by the end-dev.
type ControllerActivator struct {
	app *Application

	injector *hero.Struct

	// initRef BaseController // the BaseController as it's passed from the end-dev.
	Value reflect.Value // the BaseController's Value.
	Type  reflect.Type  // raw type of the BaseController (initRef).
	// FullName it's the last package path segment + "." + the Name.
	// i.e: if login-example/user/controller.go, the FullName is "user.Controller".
	fullName string

	// the already-registered routes, key = the controller's function name.
	// End-devs can change some properties of the *Route on the `BeforeActivator` by using the
	// `GetRoute/GetRoutes(functionName)`.
	routes map[string][]*router.Route

	// true if this controller listens and serves to websocket events.
	servesWebsocket bool

	// true to skip the internal "activate".
	activated bool
}

// NameOf returns the package name + the struct type's name,
// it's used to take the full name of an Controller, the `ControllerActivator#Name`.
func NameOf(v interface{}) string {
	elemTyp := indirectType(reflect.ValueOf(v).Type())

	typName := elemTyp.Name()
	pkgPath := elemTyp.PkgPath()
	fullname := pkgPath[strings.LastIndexByte(pkgPath, '/')+1:] + "." + typName

	return fullname
}

func newControllerActivator(app *Application, controller interface{}) *ControllerActivator {
	if controller == nil {
		return nil
	}

	if c, ok := controller.(*ControllerActivator); ok {
		return c
	}

	typ := reflect.TypeOf(controller)

	c := &ControllerActivator{
		// give access to the Router to the end-devs if they need it for some reason,
		// i.e register done handlers.
		app:   app,
		Value: reflect.ValueOf(controller),
		Type:  typ,
		// the full name of the controller: its type including the package path.
		fullName: NameOf(controller),
		// set some methods that end-dev cann't use accidentally
		// to register a route via the `Handle`,
		// all available exported and compatible methods
		// are being appended to the slice at the `parseMethods`,
		// if a new method is registered via `Handle` its function name
		// is also appended to that slice.
		routes: whatReservedMethods(typ),
	}

	return c
}

func whatReservedMethods(typ reflect.Type) map[string][]*router.Route {
	methods := []string{"BeforeActivation", "AfterActivation"}
	//  BeforeActivatior/AfterActivation are not routes but they are
	// reserved names*
	if isBaseController(typ) {
		methods = append(methods, "BeginRequest", "EndRequest")
	}

	routes := make(map[string][]*router.Route, len(methods))
	for _, m := range methods {
		routes[m] = []*router.Route{}
	}

	return routes
}

// Name returns the full name of the controller, its package name + the type name.
// Can used at both `BeforeActivation` and `AfterActivation`.
func (c *ControllerActivator) Name() string {
	return c.fullName
}

// Router is the standard Iris router's public API.
// With this you can register middleware, view layouts, subdomains, serve static files
// and even add custom standard iris handlers as normally.
//
// This Router is the router instance that came from the parent MVC Application,
// it's the `app.Party(...)` argument.
//
// Can used at both `BeforeActivation` and `AfterActivation`.
func (c *ControllerActivator) Router() router.Party {
	return c.app.Router
}

// GetRoute returns the first registered route based on the controller's method name.
// It can be used to change the route's name, which is useful for reverse routing
// inside views. Custom routes can be registered with `Handle`, which returns the *Route.
// This method exists mostly for the automatic method parsing based on the known patterns
// inside a controller.
//
// A check for `nil` is necessary for unregistered methods.
//
// See `GetRoutes` and `Handle` too.
func (c *ControllerActivator) GetRoute(methodName string) *router.Route {
	routes := c.GetRoutes(methodName)
	if len(routes) > 0 {
		return routes[0]
	}

	return nil
}

// GetRoutes returns one or more registered route based on the controller's method name.
// It can be used to change the route's name, which is useful for reverse routing
// inside views. Custom routes can be registered with `Handle`, which returns the *Route.
// This method exists mostly for the automatic method parsing based on the known patterns
// inside a controller.
//
// A check for `nil` is necessary for unregistered methods.
//
// See `Handle` too.
func (c *ControllerActivator) GetRoutes(methodName string) []*router.Route {
	for name, routes := range c.routes {
		if name == methodName {
			return routes
		}
	}
	return nil
}

// Singleton returns new if all incoming clients' requests
// have the same controller instance.
// This is done automatically by iris to reduce the creation
// of a new controller on each request, if the controller doesn't contain
// any unexported fields and all fields are services-like, static.
func (c *ControllerActivator) Singleton() bool {
	if c.injector == nil {
		panic("MVC: Singleton called from wrong state the API gives access to it only `AfterActivation`, report this as bug")
	}
	return c.injector.Singleton
}

// DependenciesReadOnly returns a list of dependencies, including the controller's one.
func (c *ControllerActivator) DependenciesReadOnly() []*hero.Dependency {
	if c.injector == nil {
		panic("MVC: DependenciesReadOnly called from wrong state the API gives access to it only `AfterActivation`, report this as bug")
	}

	return c.injector.Container.Dependencies
}

// Dependencies returns a value which can manage the controller's dependencies.
func (c *ControllerActivator) Dependencies() *hero.Container {
	return c.app.container
}

// checks if a method is already registered.
func (c *ControllerActivator) isReservedMethod(name string) bool {
	for methodName := range c.routes {
		if methodName == name {
			return true
		}
	}

	return false
}

func (c *ControllerActivator) markAsWebsocket() {
	c.servesWebsocket = true
	c.attachInjector()
}

func (c *ControllerActivator) attachInjector() {
	if c.injector == nil {
		partyCountParams := macro.CountParams(c.app.Router.GetRelPath(), *c.app.Router.Macros())
		c.injector = c.app.container.Struct(c.Value, partyCountParams)
	}
}

// Activated can be called to skip the internal method parsing.
func (c *ControllerActivator) Activated() bool {
	b := c.activated
	c.activated = true
	return b
}

func (c *ControllerActivator) activate() {
	if c.Activated() {
		return
	}

	c.parseMethods()
}

// register all available, exported methods to handlers if possible.
func (c *ControllerActivator) parseMethods() {
	n := c.Type.NumMethod()
	for i := 0; i < n; i++ {
		m := c.Type.Method(i)
		c.parseMethod(m)
	}
}

func (c *ControllerActivator) parseMethod(m reflect.Method) {
	httpMethod, httpPath, err := parseMethod(c.app.Router.Macros(), m, c.isReservedMethod)
	if err != nil {
		if err != errSkip {
			c.addErr(fmt.Errorf("MVC: fail to parse the route path and HTTP method for '%s.%s': %v", c.fullName, m.Name, err))
		}

		return
	}

	c.Handle(httpMethod, httpPath, m.Name)
}

func (c *ControllerActivator) addErr(err error) bool {
	return c.app.Router.GetReporter().Err(err) != nil
}

// Handle registers a route based on a http method, the route's path
// and a function name that belongs to the controller, it accepts
// a forth, optionally, variadic parameter which is the before handlers.
//
// Just like `Party#Handle`, it returns the `*router.Route`, if failed
// then it logs the errors and it returns nil, you can check the errors
// programmatically by the `Party#GetReporter`.
func (c *ControllerActivator) Handle(method, path, funcName string, middleware ...context.Handler) *router.Route {
	routes := c.HandleMany(method, path, funcName, middleware...)
	if len(routes) == 0 {
		return nil
	}

	return routes[0]
}

// HandleMany like `Handle` but can register more than one path and HTTP method routes
// separated by whitespace on the same controller's method.
// Keep note that if the controller's method input arguments are path parameters dependencies
// they should match with each of the given paths.
//
// Just like `Party#HandleMany`:, it returns the `[]*router.Routes`.
// Usage:
// func (*Controller) BeforeActivation(b mvc.BeforeActivation) {
// 	b.HandleMany("GET", "/path /path1" /path2", "HandlePath")
// }
func (c *ControllerActivator) HandleMany(method, path, funcName string, middleware ...context.Handler) []*router.Route {
	return c.handleMany(method, path, funcName, true, middleware...)
}

func (c *ControllerActivator) handleMany(method, path, funcName string, override bool, middleware ...context.Handler) []*router.Route {
	if method == "" || path == "" || funcName == "" ||
		c.isReservedMethod(funcName) {
		// isReservedMethod -> if it's already registered
		// by a previous Handle or analyze methods internally.
		return nil
	}

	handler := c.handlerOf(path, funcName)

	// register the handler now.
	routes := c.app.Router.HandleMany(method, path, append(middleware, handler)...)
	if routes == nil {
		c.addErr(fmt.Errorf("MVC: unable to register a route for the path for '%s.%s'", c.fullName, funcName))
		return nil
	}

	for _, r := range routes {
		// change the main handler's name and file:line
		// in order to respect the controller's and give
		// a proper debug/log message.
		r.Description = "controller"
		r.MainHandlerName = fmt.Sprintf("%s.%s", c.fullName, funcName)
		if m, ok := c.Type.MethodByName(funcName); ok {
			r.SourceFileName, r.SourceLineNumber = context.HandlerFileLineRel(m.Func)
		}
	}

	// add this as a reserved method name in order to
	// be sure that the same route
	// (method is allowed to be registered more than one on different routes - v11.2).

	existingRoutes, exist := c.routes[funcName]
	if override || !exist {
		c.routes[funcName] = routes
	} else {
		c.routes[funcName] = append(existingRoutes, routes...)
	}

	return routes
}

func (c *ControllerActivator) handlerOf(relPath, methodName string) context.Handler {
	c.attachInjector()

	fullpath := c.app.Router.GetRelPath() + relPath
	paramsCount := macro.CountParams(fullpath, *c.app.Router.Macros())
	handler := c.injector.MethodHandler(methodName, paramsCount)

	if isBaseController(c.Type) {
		return func(ctx context.Context) {
			ctrl, err := c.injector.Acquire(ctx)
			if err != nil {
				// if err != hero.ErrStopExecution {
				// 	c.injector.Container.GetErrorHandler(ctx).HandleError(ctx, err)
				// }
				c.injector.Container.GetErrorHandler(ctx).HandleError(ctx, err)
				return
			}

			b := ctrl.Interface().(BaseController)
			// init the request.
			b.BeginRequest(ctx)

			// if begin request stopped the execution.
			if ctx.IsStopped() {
				return
			}

			handler(ctx)

			b.EndRequest(ctx)
		}
	}

	return handler
}
