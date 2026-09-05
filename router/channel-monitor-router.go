package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

type channelMonitorPermissionRoute struct {
	method      string
	path        string
	permissions []authz.Permission
	handler     gin.HandlerFunc
}

func registerChannelMonitorRoutes(apiRouter *gin.RouterGroup) {
	statusRoute := apiRouter.Group("/channel-monitor")
	statusRoute.Use(middleware.UserAuth())
	statusRoute.GET("/status", controller.GetChannelMonitorStatus)

	adminRoute := apiRouter.Group("/channel-monitor")
	adminRoute.Use(middleware.AdminAuth())
	for _, route := range channelMonitorPermissionRoutes {
		handlers := make([]gin.HandlerFunc, 0, len(route.permissions)+1)
		for _, permission := range route.permissions {
			handlers = append(handlers, middleware.RequirePermission(permission))
		}
		handlers = append(handlers, route.handler)
		adminRoute.Handle(route.method, route.path, handlers...)
	}
}

var channelMonitorPermissionRoutes = []channelMonitorPermissionRoute{
	{method: http.MethodGet, path: "/", permissions: []authz.Permission{authz.ChannelRead}, handler: controller.ListChannelMonitors},
	{method: http.MethodGet, path: "/models", permissions: []authz.Permission{authz.ChannelRead}, handler: controller.ListChannelMonitorModels},
	{method: http.MethodPost, path: "/", permissions: []authz.Permission{authz.ChannelWrite, authz.ChannelOperate}, handler: controller.CreateChannelMonitor},
	{method: http.MethodPut, path: "/:id", permissions: []authz.Permission{authz.ChannelWrite, authz.ChannelOperate}, handler: controller.UpdateChannelMonitor},
	{method: http.MethodDelete, path: "/:id", permissions: []authz.Permission{authz.ChannelWrite, authz.ChannelOperate}, handler: controller.DeleteChannelMonitor},
	{method: http.MethodPost, path: "/:id/test", permissions: []authz.Permission{authz.ChannelOperate}, handler: controller.RunChannelMonitor},
	{method: http.MethodGet, path: "/:id/test/:task_id", permissions: []authz.Permission{authz.ChannelOperate}, handler: controller.GetChannelMonitorTask},
	{method: http.MethodGet, path: "/:id/runs", permissions: []authz.Permission{authz.ChannelRead}, handler: controller.ListChannelMonitorRuns},
}
