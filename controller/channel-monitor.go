package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type channelMonitorRequest struct {
	Group             string  `json:"group"`
	Model             string  `json:"model"`
	EndpointType      *string `json:"endpoint_type"`
	Enabled           *bool   `json:"enabled"`
	Interval          int     `json:"interval_seconds"`
	Timeout           int     `json:"timeout_ms"`
	ScheduleStartTime *string `json:"schedule_start_time"`
	ScheduleEndTime   *string `json:"schedule_end_time"`
}

type channelMonitorTaskPayload struct {
	MonitorID int64 `json:"monitor_id,omitempty"`
}

type channelMonitorTaskStatusView struct {
	TaskID string                 `json:"task_id"`
	Status model.SystemTaskStatus `json:"status"`
	Error  string                 `json:"error,omitempty"`
}

type channelMonitorHistoryItem struct {
	Status         string `json:"status"`
	StartedAt      int64  `json:"started_at"`
	ResponseTimeMs int64  `json:"response_time_ms"`
}

type channelMonitorView struct {
	ID                 int64                       `json:"id"`
	Group              string                      `json:"group"`
	Model              string                      `json:"model"`
	EndpointType       string                      `json:"endpoint_type,omitempty"`
	Enabled            bool                        `json:"enabled"`
	IntervalSeconds    int                         `json:"interval_seconds"`
	TimeoutMs          int                         `json:"timeout_ms"`
	ScheduleStartTime  string                      `json:"schedule_start_time"`
	ScheduleEndTime    string                      `json:"schedule_end_time"`
	LastRunAt          int64                       `json:"last_run_at"`
	LastStatus         string                      `json:"last_status"`
	LastResponseTimeMs int64                       `json:"last_response_time_ms"`
	LastChannelID      int                         `json:"last_channel_id"`
	LastChannelName    string                      `json:"last_channel_name"`
	LastPriority       int64                       `json:"last_priority"`
	LastError          string                      `json:"last_error,omitempty"`
	LastDegraded       bool                        `json:"last_degraded"`
	Availability7d     float64                     `json:"availability_7d"`
	Availability30d    float64                     `json:"availability_30d"`
	History            []channelMonitorHistoryItem `json:"history"`
	UpdatedAt          int64                       `json:"updated_at"`
}

// channelMonitorStatusView contains only the routing health data that is safe
// to show to regular users. Channel names, priorities, and schedule details
// remain available through the admin monitor API and run history.
type channelMonitorStatusView struct {
	ID                 int64                       `json:"id"`
	Group              string                      `json:"group"`
	Model              string                      `json:"model"`
	EndpointType       string                      `json:"endpoint_type,omitempty"`
	Enabled            bool                        `json:"enabled"`
	IntervalSeconds    int                         `json:"interval_seconds"`
	TimeoutMs          int                         `json:"timeout_ms"`
	LastRunAt          int64                       `json:"last_run_at"`
	LastStatus         string                      `json:"last_status"`
	LastResponseTimeMs int64                       `json:"last_response_time_ms"`
	LastDegraded       bool                        `json:"last_degraded"`
	Availability7d     float64                     `json:"availability_7d"`
	Availability30d    float64                     `json:"availability_30d"`
	History            []channelMonitorHistoryItem `json:"history"`
	UpdatedAt          int64                       `json:"updated_at"`
	UserRatio          float64                     `json:"user_ratio"`
}

func (view channelMonitorView) publicStatus(ratio float64) channelMonitorStatusView {
	return channelMonitorStatusView{
		ID:                 view.ID,
		Group:              view.Group,
		Model:              view.Model,
		EndpointType:       view.EndpointType,
		Enabled:            view.Enabled,
		IntervalSeconds:    view.IntervalSeconds,
		TimeoutMs:          view.TimeoutMs,
		LastRunAt:          view.LastRunAt,
		LastStatus:         view.LastStatus,
		LastResponseTimeMs: view.LastResponseTimeMs,
		LastDegraded:       view.LastDegraded,
		Availability7d:     view.Availability7d,
		Availability30d:    view.Availability30d,
		History:            view.History,
		UpdatedAt:          view.UpdatedAt,
		UserRatio:          ratio,
	}
}

type channelMonitorRunView struct {
	ID               int64                          `json:"id"`
	MonitorID        int64                          `json:"monitor_id"`
	Source           string                         `json:"source"`
	Status           string                         `json:"status"`
	StartedAt        int64                          `json:"started_at"`
	FinishedAt       int64                          `json:"finished_at"`
	ResponseTimeMs   int64                          `json:"response_time_ms"`
	FinalChannelID   int                            `json:"final_channel_id"`
	FinalChannelName string                         `json:"final_channel_name"`
	FinalPriority    int64                          `json:"final_priority"`
	Degraded         bool                           `json:"degraded"`
	AttemptCount     int                            `json:"attempt_count"`
	Error            string                         `json:"error,omitempty"`
	Attempts         []*model.ChannelMonitorAttempt `json:"attempts"`
}

func buildChannelMonitorView(
	monitor *model.ChannelMonitor,
	count7d model.ChannelMonitorRunCount,
	count30d model.ChannelMonitorRunCount,
	runs []*model.ChannelMonitorRun,
) channelMonitorView {
	view := channelMonitorView{
		ID:                 monitor.ID,
		Group:              monitor.Group,
		Model:              monitor.Model,
		EndpointType:       monitor.EndpointType,
		Enabled:            monitor.Enabled,
		IntervalSeconds:    monitor.IntervalSeconds,
		TimeoutMs:          monitor.TimeoutMs,
		ScheduleStartTime:  monitor.ScheduleStartTime,
		ScheduleEndTime:    monitor.ScheduleEndTime,
		LastRunAt:          monitor.LastRunAt,
		LastStatus:         monitor.LastStatus,
		LastResponseTimeMs: monitor.LastResponseTimeMs,
		LastChannelID:      monitor.LastChannelID,
		LastChannelName:    monitor.LastChannelName,
		LastPriority:       monitor.LastPriority,
		LastError:          monitor.LastError,
		LastDegraded:       monitor.LastDegraded,
		UpdatedAt:          monitor.UpdatedAt,
		History:            make([]channelMonitorHistoryItem, 0),
	}
	if count7d.Total > 0 {
		view.Availability7d = float64(count7d.Available) / float64(count7d.Total)
	}
	if count30d.Total > 0 {
		view.Availability30d = float64(count30d.Available) / float64(count30d.Total)
	}
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		view.History = append(view.History, channelMonitorHistoryItem{
			Status:         run.Status,
			StartedAt:      run.StartedAt,
			ResponseTimeMs: run.ResponseTimeMs,
		})
	}
	return view
}

func buildChannelMonitorViews(monitors []*model.ChannelMonitor, now int64) ([]channelMonitorView, error) {
	items := make([]channelMonitorView, 0, len(monitors))
	if len(monitors) == 0 {
		return items, nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	monitorIDs := make([]int64, 0, len(monitors))
	for _, monitor := range monitors {
		monitorIDs = append(monitorIDs, monitor.ID)
	}
	counts7d, err := model.CountChannelMonitorRunsForMonitors(monitorIDs, now-7*24*60*60)
	if err != nil {
		return nil, err
	}
	counts30d, err := model.CountChannelMonitorRunsForMonitors(monitorIDs, now-30*24*60*60)
	if err != nil {
		return nil, err
	}
	runsByMonitor, err := model.ListChannelMonitorRunsForMonitors(monitorIDs, model.ChannelMonitorHistoryLimit)
	if err != nil {
		return nil, err
	}
	for _, monitor := range monitors {
		runs := runsByMonitor[monitor.ID]
		items = append(items, buildChannelMonitorView(
			monitor,
			counts7d[monitor.ID],
			counts30d[monitor.ID],
			runs,
		))
	}
	return items, nil
}

func buildChannelMonitorRunView(
	run *model.ChannelMonitorRun,
	attempts []*model.ChannelMonitorAttempt,
) channelMonitorRunView {
	return channelMonitorRunView{
		ID:               run.ID,
		MonitorID:        run.MonitorID,
		Source:           model.NormalizeChannelMonitorRunSource(run.Source),
		Status:           run.Status,
		StartedAt:        run.StartedAt,
		FinishedAt:       run.FinishedAt,
		ResponseTimeMs:   run.ResponseTimeMs,
		FinalChannelID:   run.FinalChannelID,
		FinalChannelName: run.FinalChannelName,
		FinalPriority:    run.FinalPriority,
		Degraded:         run.Degraded,
		AttemptCount:     run.AttemptCount,
		Error:            run.Error,
		Attempts:         attempts,
	}
}

func normalizeChannelMonitorRequest(request channelMonitorRequest, existing *model.ChannelMonitor) *model.ChannelMonitor {
	monitor := &model.ChannelMonitor{}
	if existing != nil {
		*monitor = *existing
	}
	if request.Group != "" || existing == nil {
		monitor.Group = request.Group
	}
	if request.Model != "" || existing == nil {
		monitor.Model = request.Model
	}
	if request.EndpointType != nil {
		monitor.EndpointType = *request.EndpointType
	} else if existing == nil {
		monitor.EndpointType = ""
	}
	if request.Enabled != nil {
		monitor.Enabled = *request.Enabled
	} else if existing == nil {
		monitor.Enabled = true
	}
	if request.Interval != 0 {
		monitor.IntervalSeconds = request.Interval
	}
	if request.Timeout != 0 {
		monitor.TimeoutMs = request.Timeout
	}
	if request.ScheduleStartTime != nil {
		monitor.ScheduleStartTime = *request.ScheduleStartTime
	} else if existing == nil {
		monitor.ScheduleStartTime = ""
	}
	if request.ScheduleEndTime != nil {
		monitor.ScheduleEndTime = *request.ScheduleEndTime
	} else if existing == nil {
		monitor.ScheduleEndTime = ""
	}
	model.NormalizeChannelMonitor(monitor)
	return monitor
}

func channelMonitorTargetChanged(request channelMonitorRequest, existing *model.ChannelMonitor) bool {
	if existing == nil {
		return false
	}
	group := strings.TrimSpace(request.Group)
	if group != "" && group != existing.Group {
		return true
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName != "" && modelName != existing.Model {
		return true
	}
	if request.EndpointType == nil {
		return false
	}
	endpointType := strings.TrimSpace(*request.EndpointType)
	if strings.EqualFold(endpointType, "auto") {
		endpointType = ""
	}
	return endpointType != existing.EndpointType
}

func ListChannelMonitors(c *gin.Context) {
	monitors, err := model.ListChannelMonitors()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, err := buildChannelMonitorViews(monitors, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func ListChannelMonitorModels(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		common.ApiError(c, errors.New("group is required"))
		return
	}
	if strings.EqualFold(group, "auto") {
		common.ApiError(c, errors.New("auto is not a monitor target group"))
		return
	}
	models := model.GetGroupEnabledModels(group)
	concreteModels := make([]string, 0, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || strings.Contains(modelName, "*") {
			continue
		}
		concreteModels = append(concreteModels, modelName)
	}
	sort.Strings(concreteModels)
	common.ApiSuccess(c, gin.H{"items": concreteModels})
}

func CreateChannelMonitor(c *gin.Context) {
	var request channelMonitorRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	monitor := normalizeChannelMonitorRequest(request, nil)
	if strings.EqualFold(monitor.Group, "auto") {
		common.ApiError(c, errors.New("auto is not a monitor target group"))
		return
	}
	if existing, err := model.GetChannelMonitorByTarget(monitor.Group, monitor.Model); err != nil {
		common.ApiError(c, err)
		return
	} else if existing != nil {
		common.ApiError(c, errors.New("a monitor for this group and model already exists"))
		return
	}
	if err := model.CreateChannelMonitor(monitor); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateChannelMonitorObserverCache(monitor.Group, monitor.Model)
	common.ApiSuccess(c, monitor)
}

func UpdateChannelMonitor(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid monitor id"))
		return
	}
	monitor, err := model.GetChannelMonitor(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if monitor == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var request channelMonitorRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if channelMonitorTargetChanged(request, monitor) {
		common.ApiError(c, errors.New("monitor group, model, and endpoint type cannot be changed; delete and recreate the monitor"))
		return
	}
	updated := normalizeChannelMonitorRequest(request, monitor)
	updated.ID = monitor.ID
	if existing, err := model.GetChannelMonitorByTarget(updated.Group, updated.Model); err != nil {
		common.ApiError(c, err)
		return
	} else if existing != nil && existing.ID != id {
		common.ApiError(c, errors.New("a monitor for this group and model already exists"))
		return
	}
	if err := model.UpdateChannelMonitor(updated); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateChannelMonitorObserverCache(monitor.Group, monitor.Model)
	service.InvalidateChannelMonitorObserverCache(updated.Group, updated.Model)
	common.ApiSuccess(c, updated)
}

func DeleteChannelMonitor(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid monitor id"))
		return
	}
	monitor, err := model.GetChannelMonitor(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if monitor == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err := model.DeleteChannelMonitor(id); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateChannelMonitorObserverCache(monitor.Group, monitor.Model)
	common.ApiSuccess(c, nil)
}

func RunChannelMonitor(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid monitor id"))
		return
	}
	monitor, err := model.GetChannelMonitor(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if monitor == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	activeKey := fmt.Sprintf("%s:%d", model.SystemTaskTypeChannelMonitor, id)
	task, created, err := service.EnqueueScopedSystemTask(model.SystemTaskTypeChannelMonitor, activeKey, channelMonitorTaskPayload{
		MonitorID: id,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "a channel monitor task is already running",
			"data": gin.H{
				"task_id": task.TaskID,
				"status":  task.Status,
			},
		})
		return
	}
	common.ApiSuccess(c, gin.H{"task_id": task.TaskID, "status": task.Status})
}

func GetChannelMonitorTask(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid monitor id"))
		return
	}
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		common.ApiError(c, errors.New("invalid task id"))
		return
	}
	task, err := model.GetSystemTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil || task.Type != model.SystemTaskTypeChannelMonitor {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	payload := channelMonitorTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil || payload.MonitorID != id {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	common.ApiSuccess(c, channelMonitorTaskStatusView{
		TaskID: task.TaskID,
		Status: task.Status,
		Error:  task.Error,
	})
}

func ListChannelMonitorRuns(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid monitor id"))
		return
	}
	monitor, err := model.GetChannelMonitor(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if monitor == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	runs, err := model.ListChannelMonitorRuns(id, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	runIDs := make([]int64, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.ID)
	}
	attemptsByRun, err := model.ListChannelMonitorAttemptsForRuns(runIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]channelMonitorRunView, 0, len(runs))
	for _, run := range runs {
		items = append(items, buildChannelMonitorRunView(run, attemptsByRun[run.ID]))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func GetChannelMonitorStatus(c *gin.Context) {
	monitors, err := model.ListChannelMonitors()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userGroup, err := model.GetUserGroup(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	visibleMonitors := make([]*model.ChannelMonitor, 0, len(monitors))
	for _, monitor := range monitors {
		if !monitor.Enabled || !service.GroupInUserUsableGroups(userGroup, monitor.Group) {
			continue
		}
		visibleMonitors = append(visibleMonitors, monitor)
	}
	views, err := buildChannelMonitorViews(visibleMonitors, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]channelMonitorStatusView, 0, len(views))
	for index, view := range views {
		userRatio := service.GetUserGroupRatio(userGroup, visibleMonitors[index].Group)
		items = append(items, view.publicStatus(userRatio))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func monitorErrorMessage(result testResult) string {
	if result.newAPIError != nil {
		return strings.TrimSpace(result.newAPIError.Error())
	}
	if result.localErr != nil {
		return strings.TrimSpace(result.localErr.Error())
	}
	return "upstream returned an invalid response"
}

func channelMonitorSuccessState(firstPriority, successPriority int64, attemptCount int) (string, bool) {
	return model.ChannelMonitorSuccessState(firstPriority, successPriority, attemptCount > 1)
}

func runOneChannelMonitor(ctx context.Context, monitor *model.ChannelMonitor, testUserID int) error {
	if monitor == nil {
		return errors.New("monitor is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model.NormalizeChannelMonitor(monitor)
	startedAt := common.GetTimestamp()
	started := time.Now()
	run := &model.ChannelMonitorRun{
		MonitorID: monitor.ID,
		Source:    model.ChannelMonitorRunSourceActive,
		Status:    model.ChannelMonitorStatusError,
		StartedAt: startedAt,
	}
	candidates, err := model.GetChannelMonitorCandidatesForCheck(monitor.Group, monitor.Model, monitor.EndpointType)
	if err != nil {
		run.FinishedAt = common.GetTimestamp()
		run.ResponseTimeMs = time.Since(started).Milliseconds()
		run.Error = err.Error()
		return model.SaveChannelMonitorRun(monitor, run, nil)
	}
	if len(candidates) == 0 {
		run.Status = model.ChannelMonitorStatusFailed
		run.FinishedAt = common.GetTimestamp()
		run.ResponseTimeMs = time.Since(started).Milliseconds()
		run.Error = "no enabled channel supports this group and model"
		return model.SaveChannelMonitorRun(monitor, run, nil)
	}

	firstPriority := candidates[0].Priority
	attempts := make([]*model.ChannelMonitorAttempt, 0, len(candidates))
	attemptResults := make([]testResult, 0, len(candidates))
	attemptStreams := make([]bool, 0, len(candidates))
	for index, candidate := range candidates {
		if candidate.Channel == nil {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		attemptStarted := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(monitor.TimeoutMs)*time.Millisecond)
		endpointType := monitor.EndpointType
		if candidate.EndpointType != "" {
			endpointType = candidate.EndpointType
		}
		isStream := shouldUseStreamForAutomaticChannelTest(candidate.Channel)
		result := testChannelWithoutBilling(
			attemptCtx,
			candidate.Channel,
			testUserID,
			monitor.Model,
			endpointType,
			isStream,
			monitor.Group,
		)
		cancel()
		attempt := &model.ChannelMonitorAttempt{
			AttemptOrder:   index + 1,
			ChannelID:      candidate.Channel.Id,
			ChannelName:    candidate.Channel.Name,
			Priority:       candidate.Priority,
			Success:        result.localErr == nil && result.newAPIError == nil,
			ResponseTimeMs: time.Since(attemptStarted).Milliseconds(),
		}
		if !attempt.Success {
			attempt.Error = monitorErrorMessage(result)
		}
		attempts = append(attempts, attempt)
		attemptResults = append(attemptResults, result)
		attemptStreams = append(attemptStreams, isStream)
		if attempt.Success {
			run.Status, run.Degraded = channelMonitorSuccessState(firstPriority, candidate.Priority, len(attempts))
			run.FinalChannelID = candidate.Channel.Id
			run.FinalChannelName = candidate.Channel.Name
			run.FinalPriority = candidate.Priority
			break
		}
		run.Error = attempt.Error
	}
	run.AttemptCount = len(attempts)
	run.FinishedAt = common.GetTimestamp()
	run.ResponseTimeMs = time.Since(started).Milliseconds()
	if run.Status == model.ChannelMonitorStatusError {
		run.Status = model.ChannelMonitorStatusFailed
		if run.Error == "" {
			if ctx.Err() != nil {
				run.Error = ctx.Err().Error()
			} else {
				run.Error = "all enabled channels failed"
			}
		}
	}
	if err := model.SaveChannelMonitorRun(monitor, run, attempts); err != nil {
		return err
	}
	for index, attempt := range attempts {
		result := attemptResults[index]
		promptTokens := 0
		completionTokens := 0
		if result.usage != nil {
			promptTokens = result.usage.PromptTokens
			completionTokens = result.usage.CompletionTokens
		}
		status := model.ChannelMonitorStatusFailed
		if attempt.Success {
			status, _ = channelMonitorSuccessState(firstPriority, attempt.Priority, attempt.AttemptOrder)
		}
		if err := model.RecordChannelMonitorLog(model.RecordChannelMonitorLogParams{
			UserID:           testUserID,
			MonitorID:        monitor.ID,
			RunID:            run.ID,
			AttemptID:        attempt.ID,
			AttemptOrder:     attempt.AttemptOrder,
			ChannelID:        attempt.ChannelID,
			ChannelName:      attempt.ChannelName,
			ModelName:        monitor.Model,
			Group:            monitor.Group,
			Priority:         attempt.Priority,
			Status:           status,
			Success:          attempt.Success,
			ResponseTimeMs:   attempt.ResponseTimeMs,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			EstimatedQuota:   result.estimatedQuota,
			CostKnown:        result.costKnown,
			IsStream:         attemptStreams[index],
			Error:            attempt.Error,
			Other:            result.other,
		}); err != nil {
			common.SysError("failed to record channel monitor log: " + err.Error())
		}
		// The probe is not charged to the root/test user, but the upstream
		// request still consumes provider quota. Keep the channel's accumulated
		// usage aligned with the admin monitor log, including estimated costs
		// when the provider did not return usage details.
		if result.estimatedQuota > 0 {
			model.UpdateChannelUsedQuota(attempt.ChannelID, result.estimatedQuota)
		}
	}
	return nil
}

func runChannelMonitorTask(ctx context.Context, payload channelMonitorTaskPayload) (int, error) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return 0, err
	}
	var monitors []*model.ChannelMonitor
	if payload.MonitorID > 0 {
		monitor, getErr := model.GetChannelMonitor(payload.MonitorID)
		if getErr != nil {
			return 0, getErr
		}
		if monitor == nil {
			return 0, errors.New("monitor not found")
		}
		monitors = []*model.ChannelMonitor{monitor}
	} else {
		monitors, err = model.GetDueChannelMonitors(common.GetTimestamp())
		if err != nil {
			return 0, err
		}
	}
	processed := 0
	for _, monitor := range monitors {
		if ctx != nil && ctx.Err() != nil {
			return processed, ctx.Err()
		}

		// The scheduler takes a due snapshot before this loop. Refresh each
		// target immediately before probing because an earlier probe or real
		// traffic may have postponed it, and an administrator may have disabled
		// or edited it while this task was waiting behind another target.
		current, getErr := model.GetChannelMonitor(monitor.ID)
		if getErr != nil {
			return processed, getErr
		}
		if current == nil {
			if payload.MonitorID > 0 {
				return processed, errors.New("monitor not found")
			}
			continue
		}
		if payload.MonitorID == 0 && !model.IsChannelMonitorDue(current, common.GetTimestamp()) {
			continue
		}
		monitor = current
		if runErr := runOneChannelMonitor(ctx, monitor, testUserID); runErr != nil {
			return processed, runErr
		}
		processed++
	}
	return processed, nil
}
