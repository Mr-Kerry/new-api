package model

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ChannelMonitorStatusOperational = "operational"
	ChannelMonitorStatusDegraded    = "degraded"
	ChannelMonitorStatusFailed      = "failed"
	ChannelMonitorStatusError       = "error"

	ChannelMonitorRunSourceActive  = "active"
	ChannelMonitorRunSourceTraffic = "traffic"

	ChannelMonitorDefaultIntervalSeconds = 1800
	ChannelMonitorPassiveSampleSeconds   = 60
	// ChannelMonitorFailureConfirmDelaySeconds is the short follow-up delay
	// used after the first active failure. A second failed probe confirms the
	// outage before the monitor waits for its normal interval again.
	ChannelMonitorFailureConfirmDelaySeconds = 120
	ChannelMonitorDefaultTimeoutMs           = 15000
	ChannelMonitorMinIntervalSeconds         = 30
	ChannelMonitorMaxIntervalSeconds         = 86400
	ChannelMonitorMinTimeoutMs               = 1000
	ChannelMonitorMaxTimeoutMs               = 120000
	ChannelMonitorScheduleTimeLayout         = "15:04"
	ChannelMonitorHistoryLimit               = 18
	// ChannelMonitorHistoryRetentionSeconds bounds the durable probe history.
	// The API only exposes a short recent window, while availability reports
	// need up to 30 days of samples.
	ChannelMonitorHistoryRetentionSeconds int64 = 30 * 24 * 60 * 60
	channelMonitorHistoryCleanupBatchSize       = 500
)

var channelMonitorBeijingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// ChannelMonitor is a health target identified by one routing group and model.
// The unique target means a model is monitored once per routing group, while
// the actual channel selection remains dynamic and follows Ability priority.
type ChannelMonitor struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	Group             string `json:"group" gorm:"type:varchar(64);not null;uniqueIndex:idx_channel_monitor_target,priority:1"`
	Model             string `json:"model" gorm:"type:varchar(255);not null;uniqueIndex:idx_channel_monitor_target,priority:2"`
	EndpointType      string `json:"endpoint_type,omitempty" gorm:"type:varchar(64)"`
	Enabled           bool   `json:"enabled"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutMs         int    `json:"timeout_ms"`
	ScheduleStartTime string `json:"schedule_start_time" gorm:"type:varchar(5)"`
	ScheduleEndTime   string `json:"schedule_end_time" gorm:"type:varchar(5)"`
	LastRunAt         int64  `json:"last_run_at" gorm:"bigint;index"`
	// LastTrafficAt is the most recent eligible real request. It is separate
	// from LastPassiveAt because frequent traffic must postpone the active
	// fallback even when no new passive run is persisted in the current minute.
	LastTrafficAt int64 `json:"-" gorm:"bigint;index"`
	// LastPassiveAt is the most recent persisted passive sample and controls the
	// one-minute write cadence for traffic-derived runs.
	LastPassiveAt      int64  `json:"-" gorm:"bigint"`
	LastStatus         string `json:"last_status" gorm:"type:varchar(32);index"`
	LastResponseTimeMs int64  `json:"last_response_time_ms" gorm:"bigint"`
	LastChannelID      int    `json:"last_channel_id"`
	LastChannelName    string `json:"last_channel_name" gorm:"type:varchar(255)"`
	LastPriority       int64  `json:"last_priority" gorm:"bigint"`
	LastError          string `json:"last_error" gorm:"type:text"`
	LastDegraded       bool   `json:"last_degraded"`
	// FailureConfirmAt is set after the first failed active probe. It is an
	// internal scheduler marker and is deliberately not exposed in API JSON.
	FailureConfirmAt int64 `json:"-" gorm:"bigint;index"`
	// FailureConfirmed distinguishes a confirmed outage from the short
	// confirmation window after a first failure. It prevents every later
	// interval from triggering another confirmation probe until recovery.
	FailureConfirmed bool  `json:"-"`
	CreatedAt        int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt        int64 `json:"updated_at" gorm:"bigint"`
}

type ChannelMonitorRun struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	MonitorID        int64  `json:"monitor_id" gorm:"index"`
	Source           string `json:"source" gorm:"type:varchar(16);index"`
	Status           string `json:"status" gorm:"type:varchar(32);index"`
	StartedAt        int64  `json:"started_at" gorm:"bigint;index"`
	FinishedAt       int64  `json:"finished_at" gorm:"bigint"`
	ResponseTimeMs   int64  `json:"response_time_ms" gorm:"bigint"`
	FinalChannelID   int    `json:"final_channel_id"`
	FinalChannelName string `json:"final_channel_name" gorm:"type:varchar(255)"`
	FinalPriority    int64  `json:"final_priority" gorm:"bigint"`
	Degraded         bool   `json:"degraded"`
	AttemptCount     int    `json:"attempt_count"`
	Error            string `json:"error" gorm:"type:text"`
}

type ChannelMonitorAttempt struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	RunID          int64  `json:"run_id" gorm:"index"`
	AttemptOrder   int    `json:"attempt_order"`
	ChannelID      int    `json:"channel_id"`
	ChannelName    string `json:"channel_name" gorm:"type:varchar(255)"`
	Priority       int64  `json:"priority" gorm:"bigint"`
	Success        bool   `json:"success"`
	ResponseTimeMs int64  `json:"response_time_ms" gorm:"bigint"`
	Error          string `json:"error" gorm:"type:text"`
}

type ChannelMonitorCandidate struct {
	Channel      *Channel
	Priority     int64
	EndpointType string
	weight       int
}

// channelMonitorCandidatePriority follows the same source used by the live
// router. The in-memory router reads priority from Channel, while the
// database-backed router reads the denormalized Ability value. The caller
// explicitly supplies which snapshot produced the candidate so a DB fallback
// is not accidentally affected by the global cache setting.
func channelMonitorCandidatePriority(ability Ability, channel *Channel, fromCache bool) int64 {
	if fromCache && channel != nil && channel.Priority != nil {
		return channel.GetPriority()
	}
	if ability.Priority != nil {
		return *ability.Priority
	}
	return 0
}

func channelMonitorCandidateWeight(ability Ability, channel *Channel, fromCache bool) int {
	if fromCache && channel != nil && channel.Weight != nil {
		return channel.GetWeight()
	}
	return channelWeightFromUint(ability.Weight)
}

type RecordChannelMonitorTrafficSuccessParams struct {
	Group          string
	Model          string
	EndpointType   string
	ChannelID      int
	StartedAt      int64
	FinishedAt     int64
	ResponseTimeMs int64
	Retried        bool
}

type RecordChannelMonitorTrafficSuccessResult struct {
	MonitorFound  bool
	Recorded      bool
	NextAllowedAt int64
}

func (monitor *ChannelMonitor) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if monitor.CreatedAt == 0 {
		monitor.CreatedAt = now
	}
	if monitor.UpdatedAt == 0 {
		monitor.UpdatedAt = now
	}
	NormalizeChannelMonitor(monitor)
	return nil
}

func (monitor *ChannelMonitor) BeforeUpdate(_ *gorm.DB) error {
	monitor.UpdatedAt = common.GetTimestamp()
	NormalizeChannelMonitor(monitor)
	return nil
}

func NormalizeChannelMonitor(monitor *ChannelMonitor) {
	if monitor == nil {
		return
	}
	monitor.Group = strings.TrimSpace(monitor.Group)
	monitor.Model = strings.TrimSpace(monitor.Model)
	monitor.EndpointType = strings.TrimSpace(monitor.EndpointType)
	if strings.EqualFold(monitor.EndpointType, "auto") {
		monitor.EndpointType = ""
	}
	monitor.ScheduleStartTime = strings.TrimSpace(monitor.ScheduleStartTime)
	monitor.ScheduleEndTime = strings.TrimSpace(monitor.ScheduleEndTime)
	if monitor.IntervalSeconds <= 0 {
		monitor.IntervalSeconds = ChannelMonitorDefaultIntervalSeconds
	}
	if monitor.IntervalSeconds < ChannelMonitorMinIntervalSeconds {
		monitor.IntervalSeconds = ChannelMonitorMinIntervalSeconds
	}
	if monitor.IntervalSeconds > ChannelMonitorMaxIntervalSeconds {
		monitor.IntervalSeconds = ChannelMonitorMaxIntervalSeconds
	}
	if monitor.TimeoutMs <= 0 {
		monitor.TimeoutMs = ChannelMonitorDefaultTimeoutMs
	}
	if monitor.TimeoutMs < ChannelMonitorMinTimeoutMs {
		monitor.TimeoutMs = ChannelMonitorMinTimeoutMs
	}
	if monitor.TimeoutMs > ChannelMonitorMaxTimeoutMs {
		monitor.TimeoutMs = ChannelMonitorMaxTimeoutMs
	}
}

func ValidateChannelMonitor(monitor *ChannelMonitor) error {
	if monitor == nil {
		return errors.New("monitor is required")
	}
	NormalizeChannelMonitor(monitor)
	if monitor.Group == "" {
		return errors.New("group is required")
	}
	if utf8.RuneCountInString(monitor.Group) > 64 {
		return errors.New("group must not exceed 64 characters")
	}
	if monitor.Model == "" {
		return errors.New("model is required")
	}
	if utf8.RuneCountInString(monitor.Model) > 255 {
		return errors.New("model must not exceed 255 characters")
	}
	if strings.Contains(monitor.Model, "*") {
		return errors.New("model must be a concrete upstream model name, not a routing wildcard")
	}
	if utf8.RuneCountInString(monitor.EndpointType) > 64 {
		return errors.New("endpoint type must not exceed 64 characters")
	}
	if !isSupportedChannelMonitorEndpoint(monitor.EndpointType) {
		return errors.New("endpoint type is not supported by channel monitoring")
	}
	if err := validateChannelMonitorSchedule(monitor); err != nil {
		return err
	}
	return nil
}

func isSupportedChannelMonitorEndpoint(endpointType string) bool {
	switch constant.EndpointType(strings.TrimSpace(endpointType)) {
	case "",
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeJinaRerank,
		constant.EndpointTypeImageGeneration,
		constant.EndpointTypeEmbeddings:
		return true
	default:
		return false
	}
}

func validateChannelMonitorSchedule(monitor *ChannelMonitor) error {
	if monitor == nil {
		return errors.New("monitor is required")
	}
	start := strings.TrimSpace(monitor.ScheduleStartTime)
	end := strings.TrimSpace(monitor.ScheduleEndTime)
	if (start == "") != (end == "") {
		return errors.New("schedule start and end times must be set together")
	}
	if start == "" {
		return nil
	}
	if !isValidChannelMonitorScheduleTime(start) {
		return errors.New("schedule start time must use HH:MM format")
	}
	if !isValidChannelMonitorScheduleTime(end) {
		return errors.New("schedule end time must use HH:MM format")
	}
	return nil
}

func isValidChannelMonitorScheduleTime(value string) bool {
	if len(value) != len(ChannelMonitorScheduleTimeLayout) || value[2] != ':' {
		return false
	}
	for index, char := range value {
		if index == 2 {
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	_, err := time.Parse(ChannelMonitorScheduleTimeLayout, value)
	return err == nil
}

// IsChannelMonitorWithinSchedule reports whether an automatic check is allowed
// at now. Empty times mean all-day monitoring. The end of a non-empty window is
// exclusive, and a window whose start equals its end is treated as all-day.
func IsChannelMonitorWithinSchedule(monitor *ChannelMonitor, now int64) bool {
	if monitor == nil {
		return false
	}
	start := strings.TrimSpace(monitor.ScheduleStartTime)
	end := strings.TrimSpace(monitor.ScheduleEndTime)
	if start == "" && end == "" {
		return true
	}
	if (start == "") != (end == "") {
		return false
	}
	startTime, startErr := time.Parse(ChannelMonitorScheduleTimeLayout, start)
	endTime, endErr := time.Parse(ChannelMonitorScheduleTimeLayout, end)
	if startErr != nil || endErr != nil {
		return false
	}
	startMinutes := startTime.Hour()*60 + startTime.Minute()
	endMinutes := endTime.Hour()*60 + endTime.Minute()
	if startMinutes == endMinutes {
		return true
	}
	local := time.Unix(now, 0).In(channelMonitorBeijingLocation)
	currentMinutes := local.Hour()*60 + local.Minute()
	if startMinutes < endMinutes {
		return currentMinutes >= startMinutes && currentMinutes < endMinutes
	}
	return currentMinutes >= startMinutes || currentMinutes < endMinutes
}

func ListChannelMonitors() ([]*ChannelMonitor, error) {
	var monitors []*ChannelMonitor
	err := DB.Order(clause.OrderByColumn{Column: clause.Column{Name: "group"}}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "model"}}).
		Find(&monitors).Error
	return monitors, err
}

func GetChannelMonitor(id int64) (*ChannelMonitor, error) {
	var monitor ChannelMonitor
	err := DB.First(&monitor, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &monitor, err
}

func GetChannelMonitorByTarget(group, modelName string) (*ChannelMonitor, error) {
	var monitor ChannelMonitor
	err := DB.Where(&ChannelMonitor{Group: strings.TrimSpace(group), Model: strings.TrimSpace(modelName)}).First(&monitor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &monitor, err
}

func HasDueChannelMonitors(now int64) bool {
	if DB == nil {
		return false
	}
	monitors, err := GetDueChannelMonitors(now)
	return err == nil && len(monitors) > 0
}

// IsChannelMonitorDue evaluates a monitor against the current scheduler time.
// Keeping this predicate separate lets a queued task re-check a monitor after
// earlier probes or real traffic have changed its state.
func IsChannelMonitorDue(monitor *ChannelMonitor, now int64) bool {
	if monitor == nil || !monitor.Enabled {
		return false
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	NormalizeChannelMonitor(monitor)
	if !IsChannelMonitorWithinSchedule(monitor, now) {
		return false
	}
	if monitor.FailureConfirmAt > 0 {
		return now >= monitor.FailureConfirmAt
	}
	lastActivityAt := monitor.LastRunAt
	if monitor.LastTrafficAt > lastActivityAt {
		lastActivityAt = monitor.LastTrafficAt
	}
	idleThreshold := int64(monitor.IntervalSeconds)
	if monitor.LastTrafficAt > 0 {
		// Passive traffic is intentionally persisted at most once per sample
		// window. Add that maximum staleness before scheduling an active probe.
		idleThreshold += ChannelMonitorPassiveSampleSeconds
	}
	return lastActivityAt == 0 || now-lastActivityAt >= idleThreshold
}

func GetDueChannelMonitors(now int64) ([]*ChannelMonitor, error) {
	var monitors []*ChannelMonitor
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	// The interval is stored per target, so the due check is intentionally
	// expressed in Go after the indexed enabled query for DB portability.
	if err := DB.Where("enabled = ?", true).Find(&monitors).Error; err != nil {
		return nil, err
	}
	due := make([]*ChannelMonitor, 0, len(monitors))
	for _, monitor := range monitors {
		if IsChannelMonitorDue(monitor, now) {
			due = append(due, monitor)
		}
	}
	return due, nil
}

func GetChannelMonitorCandidates(group, modelName string) ([]ChannelMonitorCandidate, error) {
	return getChannelMonitorCandidates(DB, group, modelName)
}

func getChannelMonitorCandidates(db *gorm.DB, group, modelName string) ([]ChannelMonitorCandidate, error) {
	return getChannelMonitorCandidatesForCheck(db, group, modelName, "", false, true)
}

// GetChannelMonitorCandidatesForCheck returns the enabled channels that can be
// tested through the requested endpoint. When endpointType is empty, an
// Advanced Custom channel uses its first configured endpoint for the model;
// this keeps automatic checks aligned with the route the channel actually
// exposes. Ordinary channels retain the channel-test endpoint inference.
func GetChannelMonitorCandidatesForCheck(group, modelName, endpointType string) ([]ChannelMonitorCandidate, error) {
	return getChannelMonitorCandidatesForCheck(DB, group, modelName, endpointType, true, true)
}

func getChannelMonitorCandidatesForCheck(db *gorm.DB, group, modelName, endpointType string, resolveEndpoint, allowCache bool) ([]ChannelMonitorCandidate, error) {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	endpointType = strings.TrimSpace(endpointType)
	if strings.EqualFold(endpointType, "auto") {
		endpointType = ""
	}
	if group == "" || modelName == "" {
		return []ChannelMonitorCandidate{}, nil
	}
	if !isSupportedChannelMonitorEndpoint(endpointType) {
		return nil, errors.New("endpoint type is not supported by channel monitoring")
	}
	if allowCache && common.MemoryCacheEnabled {
		if candidates, loaded := getChannelMonitorCandidatesFromCache(group, modelName, endpointType, resolveEndpoint); loaded {
			return candidates, nil
		}
	}
	var abilities []Ability
	if err := db.Where(&Ability{Group: group, Model: modelName, Enabled: true}).
		Order("channel_id ASC").Find(&abilities).Error; err != nil {
		return nil, err
	}
	candidates, err := buildChannelMonitorCandidatesFromAbilities(db, abilities, modelName, endpointType, resolveEndpoint)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(modelName)
		if normalizedModel != "" && normalizedModel != modelName {
			abilities = nil
			if err := db.Where(&Ability{Group: group, Model: normalizedModel, Enabled: true}).
				Order("channel_id ASC").Find(&abilities).Error; err != nil {
				return nil, err
			}
			candidates, err = buildChannelMonitorCandidatesFromAbilities(db, abilities, modelName, endpointType, resolveEndpoint)
			if err != nil {
				return nil, err
			}
		}
	}
	sortChannelMonitorCandidates(candidates)
	return candidates, nil
}

func buildChannelMonitorCandidatesFromAbilities(db *gorm.DB, abilities []Ability, modelName, endpointType string, resolveEndpoint bool) ([]ChannelMonitorCandidate, error) {
	if len(abilities) == 0 {
		return []ChannelMonitorCandidate{}, nil
	}
	ids := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		ids = append(ids, ability.ChannelId)
	}
	var channels []*Channel
	if err := db.Where("id IN ? AND status = ?", ids, common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	channelByID := make(map[int]*Channel, len(channels))
	for _, channel := range channels {
		channelByID[channel.Id] = channel
	}
	candidates := make([]ChannelMonitorCandidate, 0, len(abilities))
	for _, ability := range abilities {
		channel := channelByID[ability.ChannelId]
		if channel == nil {
			continue
		}
		candidateEndpoint, supported := resolveChannelMonitorCandidateEndpoint(channel, modelName, endpointType, resolveEndpoint)
		if !supported {
			continue
		}
		candidates = append(candidates, ChannelMonitorCandidate{
			Channel:      channel,
			Priority:     channelMonitorCandidatePriority(ability, channel, false),
			EndpointType: candidateEndpoint,
			weight:       channelMonitorCandidateWeight(ability, channel, false),
		})
	}
	return candidates, nil
}

func sortChannelMonitorCandidates(candidates []ChannelMonitorCandidate) {
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Priority != candidates[right].Priority {
			return candidates[left].Priority > candidates[right].Priority
		}
		if candidates[left].weight != candidates[right].weight {
			return candidates[left].weight > candidates[right].weight
		}
		return candidates[left].Channel.Id < candidates[right].Channel.Id
	})
}

// getChannelMonitorCandidatesFromCache mirrors the in-memory router's model
// snapshot. The database-backed candidate query can briefly disagree with the
// live route while a cache refresh is pending, which would make monitor status
// describe a channel the next request cannot actually select. A nil cache is
// treated as not loaded so startup/tests can safely fall back to the DB path.
func getChannelMonitorCandidatesFromCache(group, modelName, endpointType string, resolveEndpoint bool) ([]ChannelMonitorCandidate, bool) {
	channelSyncLock.RLock()
	if group2model2channels == nil || channelsIDM == nil {
		channelSyncLock.RUnlock()
		return nil, false
	}
	model2channels := group2model2channels[group]
	exactIDs := model2channels[modelName]
	exactChannels := make([]*Channel, 0, len(exactIDs))
	for _, channelID := range exactIDs {
		if channel, ok := channelsIDM[channelID]; ok {
			exactChannels = append(exactChannels, channel)
		}
	}
	normalizedModel := ratio_setting.FormatMatchingModelName(modelName)
	var normalizedChannels []*Channel
	if normalizedModel != "" && normalizedModel != modelName {
		normalizedIDs := model2channels[normalizedModel]
		normalizedChannels = make([]*Channel, 0, len(normalizedIDs))
		for _, channelID := range normalizedIDs {
			if channel, ok := channelsIDM[channelID]; ok {
				normalizedChannels = append(normalizedChannels, channel)
			}
		}
	}
	channelSyncLock.RUnlock()

	candidates := buildChannelMonitorCandidatesFromChannels(exactChannels, modelName, endpointType, resolveEndpoint)
	if len(candidates) == 0 {
		candidates = buildChannelMonitorCandidatesFromChannels(normalizedChannels, modelName, endpointType, resolveEndpoint)
	}
	sortChannelMonitorCandidates(candidates)
	return candidates, true
}

func buildChannelMonitorCandidatesFromChannels(channels []*Channel, modelName, endpointType string, resolveEndpoint bool) []ChannelMonitorCandidate {
	candidates := make([]ChannelMonitorCandidate, 0, len(channels))
	for _, channel := range channels {
		candidateEndpoint, supported := resolveChannelMonitorCandidateEndpoint(channel, modelName, endpointType, resolveEndpoint)
		if !supported {
			continue
		}
		candidates = append(candidates, ChannelMonitorCandidate{
			Channel:      channel,
			Priority:     channelMonitorCandidatePriority(Ability{}, channel, true),
			EndpointType: candidateEndpoint,
			weight:       channelMonitorCandidateWeight(Ability{}, channel, true),
		})
	}
	return candidates
}

func resolveChannelMonitorCandidateEndpoint(channel *Channel, modelName, endpointType string, resolveEndpoint bool) (string, bool) {
	if !resolveEndpoint || channel == nil || channel.Type != constant.ChannelTypeAdvancedCustom {
		return endpointType, true
	}

	config := channel.GetOtherSettings().AdvancedCustom
	if config == nil {
		return "", false
	}
	if endpointType != "" {
		endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType))
		if !ok {
			// Preserve the existing channel-test behavior for an unknown custom
			// endpoint; the test path will return its normal validation error.
			return endpointType, true
		}
		requestPath := strings.ReplaceAll(endpointInfo.Path, "{model}", modelName)
		if !config.SupportsPathForModel(requestPath, modelName) {
			return "", false
		}
		return endpointType, true
	}

	endpoints := config.SupportedEndpointTypesForModel(modelName)
	for _, candidateEndpoint := range endpoints {
		// Alpha Search has a distinct request/response protocol and the
		// channel-test probe does not implement it yet. Do not accidentally
		// send a chat-completions probe to an Alpha Search route; try the next
		// configured endpoint for this model instead.
		if isSupportedChannelMonitorEndpoint(string(candidateEndpoint)) {
			return string(candidateEndpoint), true
		}
	}
	return "", false
}

func NormalizeChannelMonitorRunSource(source string) string {
	if source == ChannelMonitorRunSourceTraffic {
		return ChannelMonitorRunSourceTraffic
	}
	return ChannelMonitorRunSourceActive
}

// ChannelMonitorSuccessState reports degraded service whenever the request had
// to retry or move below the highest available routing priority.
func ChannelMonitorSuccessState(firstPriority, successPriority int64, retried bool) (string, bool) {
	degraded := retried || successPriority < firstPriority
	if degraded {
		return ChannelMonitorStatusDegraded, true
	}
	return ChannelMonitorStatusOperational, false
}

// RecordChannelMonitorTrafficSuccess uses a successful routed request as the
// health sample for an enabled group/model monitor. Passive samples have their
// own one-minute cadence; IntervalSeconds only controls when an active fallback
// is due after the last recorded sample. The monitor row lock keeps multiple
// application instances from filling the same passive sample window.
func RecordChannelMonitorTrafficSuccess(params RecordChannelMonitorTrafficSuccessParams) (RecordChannelMonitorTrafficSuccessResult, error) {
	result := RecordChannelMonitorTrafficSuccessResult{}
	params.Group = strings.TrimSpace(params.Group)
	params.Model = strings.TrimSpace(params.Model)
	params.EndpointType = strings.TrimSpace(params.EndpointType)
	if params.Group == "" || params.Model == "" || params.EndpointType == "" || params.ChannelID <= 0 {
		return result, errors.New("group, model, endpoint type, and channel are required")
	}
	if !isSupportedChannelMonitorEndpoint(params.EndpointType) {
		return result, errors.New("endpoint type is not supported by channel monitoring")
	}
	if params.FinishedAt <= 0 {
		params.FinishedAt = common.GetTimestamp()
	}
	if params.StartedAt <= 0 || params.StartedAt > params.FinishedAt {
		params.StartedAt = params.FinishedAt
	}
	if params.ResponseTimeMs < 0 {
		params.ResponseTimeMs = 0
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var monitor ChannelMonitor
		err := lockForUpdate(tx).
			Where(&ChannelMonitor{Group: params.Group, Model: params.Model, Enabled: true}).
			First(&monitor).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		result.MonitorFound = true
		NormalizeChannelMonitor(&monitor)
		if !IsChannelMonitorWithinSchedule(&monitor, params.FinishedAt) {
			result.NextAllowedAt = params.FinishedAt + ChannelMonitorPassiveSampleSeconds
			return nil
		}
		if monitor.EndpointType != "" && monitor.EndpointType != params.EndpointType {
			result.NextAllowedAt = params.FinishedAt + ChannelMonitorPassiveSampleSeconds
			return nil
		}
		candidates, err := getChannelMonitorCandidatesForCheck(
			tx,
			params.Group,
			params.Model,
			params.EndpointType,
			true,
			false,
		)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			result.NextAllowedAt = params.FinishedAt + ChannelMonitorPassiveSampleSeconds
			return nil
		}
		var finalCandidate *ChannelMonitorCandidate
		for index := range candidates {
			if candidates[index].Channel != nil && candidates[index].Channel.Id == params.ChannelID {
				finalCandidate = &candidates[index]
				break
			}
		}
		if finalCandidate == nil {
			result.NextAllowedAt = params.FinishedAt + ChannelMonitorPassiveSampleSeconds
			return nil
		}

		// Every eligible request postpones the active fallback, even when the
		// passive sample for this minute has already been recorded.
		if params.FinishedAt > monitor.LastTrafficAt {
			if err := tx.Model(&ChannelMonitor{}).Where("id = ?", monitor.ID).
				Update("last_traffic_at", params.FinishedAt).Error; err != nil {
				return err
			}
			monitor.LastTrafficAt = params.FinishedAt
		}
		if monitor.LastPassiveAt > 0 {
			result.NextAllowedAt = monitor.LastPassiveAt + ChannelMonitorPassiveSampleSeconds
			if params.FinishedAt < result.NextAllowedAt {
				return nil
			}
		}

		status, degraded := ChannelMonitorSuccessState(candidates[0].Priority, finalCandidate.Priority, params.Retried)
		run := &ChannelMonitorRun{
			MonitorID:        monitor.ID,
			Source:           ChannelMonitorRunSourceTraffic,
			Status:           status,
			StartedAt:        params.StartedAt,
			FinishedAt:       params.FinishedAt,
			ResponseTimeMs:   params.ResponseTimeMs,
			FinalChannelID:   finalCandidate.Channel.Id,
			FinalChannelName: finalCandidate.Channel.Name,
			FinalPriority:    finalCandidate.Priority,
			Degraded:         degraded,
			AttemptCount:     1,
		}
		attempt := &ChannelMonitorAttempt{
			AttemptOrder:   1,
			ChannelID:      finalCandidate.Channel.Id,
			ChannelName:    finalCandidate.Channel.Name,
			Priority:       finalCandidate.Priority,
			Success:        true,
			ResponseTimeMs: params.ResponseTimeMs,
		}
		if err := saveChannelMonitorRun(tx, &monitor, run, []*ChannelMonitorAttempt{attempt}); err != nil {
			return err
		}
		result.Recorded = true
		result.NextAllowedAt = params.FinishedAt + ChannelMonitorPassiveSampleSeconds
		return nil
	})
	return result, err
}

func CreateChannelMonitor(monitor *ChannelMonitor) error {
	if err := ValidateChannelMonitor(monitor); err != nil {
		return err
	}
	return DB.Create(monitor).Error
}

func UpdateChannelMonitor(monitor *ChannelMonitor) error {
	if err := ValidateChannelMonitor(monitor); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// Serialize configuration updates with monitor deletion and run
		// persistence. A blind UPDATE would report success after a concurrent
		// delete because GORM treats zero affected rows as a nil error.
		var current ChannelMonitor
		if err := lockForUpdate(tx).Select("id").Where("id = ?", monitor.ID).First(&current).Error; err != nil {
			return err
		}
		return tx.Model(&ChannelMonitor{}).Where("id = ?", current.ID).Updates(map[string]any{
			"group":               monitor.Group,
			"model":               monitor.Model,
			"endpoint_type":       monitor.EndpointType,
			"enabled":             monitor.Enabled,
			"interval_seconds":    monitor.IntervalSeconds,
			"timeout_ms":          monitor.TimeoutMs,
			"schedule_start_time": monitor.ScheduleStartTime,
			"schedule_end_time":   monitor.ScheduleEndTime,
			"updated_at":          common.GetTimestamp(),
		}).Error
	})
}

func DeleteChannelMonitor(id int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		// Serialize deletion with run persistence. saveChannelMonitorRun locks
		// this same row before creating history, so a late probe either commits
		// before the cleanup or observes that the monitor no longer exists.
		var monitor ChannelMonitor
		if err := lockForUpdate(tx).Select("id").Where("id = ?", id).First(&monitor).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var runIDs []int64
		if err := tx.Model(&ChannelMonitorRun{}).Where("monitor_id = ?", id).Pluck("id", &runIDs).Error; err != nil {
			return err
		}
		if len(runIDs) > 0 {
			if err := tx.Where("run_id IN ?", runIDs).Delete(&ChannelMonitorAttempt{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("monitor_id = ?", id).Delete(&ChannelMonitorRun{}).Error; err != nil {
			return err
		}
		return tx.Delete(&ChannelMonitor{}, "id = ?", id).Error
	})
}

// CleanupChannelMonitorHistory removes old attempts before their parent runs.
// It is deliberately batch-based so a busy installation does not create one
// oversized DELETE statement or hold a transaction for the entire history.
func CleanupChannelMonitorHistory(now int64) (int64, error) {
	return CleanupChannelMonitorHistoryContext(context.Background(), now)
}

// CleanupChannelMonitorHistoryContext removes history in independent batches
// and checks cancellation between destructive transactions.
func CleanupChannelMonitorHistoryContext(ctx context.Context, now int64) (int64, error) {
	if DB == nil {
		return 0, errors.New("database is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	cutoff := now - ChannelMonitorHistoryRetentionSeconds
	var deleted int64
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		batchDeleted, batchSize, err := cleanupChannelMonitorHistoryBatch(ctx, cutoff)
		deleted += batchDeleted
		if err != nil {
			return deleted, err
		}
		if batchSize == 0 || batchSize < channelMonitorHistoryCleanupBatchSize {
			return deleted, nil
		}
	}
}

func cleanupChannelMonitorHistoryBatch(ctx context.Context, cutoff int64) (int64, int, error) {
	var deleted int64
	var batchSize int
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var runIDs []int64
		if err := tx.Model(&ChannelMonitorRun{}).
			Where("(finished_at > 0 AND finished_at < ?) OR (finished_at = 0 AND started_at < ?)", cutoff, cutoff).
			Order("finished_at, id").
			Limit(channelMonitorHistoryCleanupBatchSize).
			Pluck("id", &runIDs).Error; err != nil {
			return err
		}
		batchSize = len(runIDs)
		if batchSize == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tx.Where("run_id IN ?", runIDs).Delete(&ChannelMonitorAttempt{}).Error; err != nil {
			return err
		}
		result := tx.Where("id IN ?", runIDs).Delete(&ChannelMonitorRun{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected
		return nil
	})
	// GORM reports commit failures after the transaction callback has returned.
	// Do not report rows as deleted unless the batch actually committed.
	if err != nil {
		return 0, batchSize, err
	}
	return deleted, batchSize, err
}

func ListChannelMonitorRuns(monitorID int64, limit int) ([]*ChannelMonitorRun, error) {
	runsByMonitor, err := ListChannelMonitorRunsForMonitors([]int64{monitorID}, limit)
	if err != nil {
		return nil, err
	}
	return runsByMonitor[monitorID], nil
}

// ListChannelMonitorRunsForMonitors returns the newest limit runs per monitor
// in one portable query. The correlated count avoids window functions, which
// are unavailable on some supported MySQL 5.7 installations.
func ListChannelMonitorRunsForMonitors(monitorIDs []int64, limit int) (map[int64][]*ChannelMonitorRun, error) {
	grouped := make(map[int64][]*ChannelMonitorRun, len(monitorIDs))
	uniqueMonitorIDs := make([]int64, 0, len(monitorIDs))
	seen := make(map[int64]struct{}, len(monitorIDs))
	for _, monitorID := range monitorIDs {
		if monitorID <= 0 {
			continue
		}
		if _, exists := seen[monitorID]; exists {
			continue
		}
		seen[monitorID] = struct{}{}
		uniqueMonitorIDs = append(uniqueMonitorIDs, monitorID)
		grouped[monitorID] = make([]*ChannelMonitorRun, 0)
	}
	if len(uniqueMonitorIDs) == 0 {
		return grouped, nil
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	var runs []*ChannelMonitorRun
	query := DB.Table("channel_monitor_runs AS current_run").
		Select("current_run.*").
		Where("current_run.monitor_id IN ?", uniqueMonitorIDs).
		Where(`(
			SELECT COUNT(*)
			FROM channel_monitor_runs AS newer_run
			WHERE newer_run.monitor_id = current_run.monitor_id
			  AND (
				newer_run.started_at > current_run.started_at
				OR (newer_run.started_at = current_run.started_at AND newer_run.id > current_run.id)
			  )
		) < ?`, limit).
		Order("current_run.monitor_id").
		Order("current_run.started_at desc, current_run.id desc")
	if err := query.Find(&runs).Error; err != nil {
		return nil, err
	}
	for _, run := range runs {
		grouped[run.MonitorID] = append(grouped[run.MonitorID], run)
	}
	return grouped, nil
}

func ListChannelMonitorAttemptsForRuns(runIDs []int64) (map[int64][]*ChannelMonitorAttempt, error) {
	grouped := make(map[int64][]*ChannelMonitorAttempt, len(runIDs))
	uniqueRunIDs := make([]int64, 0, len(runIDs))
	seen := make(map[int64]struct{}, len(runIDs))
	for _, runID := range runIDs {
		if runID <= 0 {
			continue
		}
		if _, exists := seen[runID]; exists {
			continue
		}
		seen[runID] = struct{}{}
		uniqueRunIDs = append(uniqueRunIDs, runID)
		grouped[runID] = make([]*ChannelMonitorAttempt, 0)
	}
	if len(uniqueRunIDs) == 0 {
		return grouped, nil
	}

	var attempts []*ChannelMonitorAttempt
	err := DB.Where("run_id IN ?", uniqueRunIDs).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "run_id"}}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "attempt_order"}}).
		Find(&attempts).Error
	if err != nil {
		return nil, err
	}
	for _, attempt := range attempts {
		grouped[attempt.RunID] = append(grouped[attempt.RunID], attempt)
	}
	return grouped, nil
}

func ListChannelMonitorAttempts(runID int64) ([]*ChannelMonitorAttempt, error) {
	grouped, err := ListChannelMonitorAttemptsForRuns([]int64{runID})
	if err != nil {
		return nil, err
	}
	return grouped[runID], nil
}

type ChannelMonitorRunCount struct {
	Total     int64
	Available int64
}

func CountChannelMonitorRunsForMonitors(monitorIDs []int64, since int64) (map[int64]ChannelMonitorRunCount, error) {
	counts := make(map[int64]ChannelMonitorRunCount, len(monitorIDs))
	uniqueMonitorIDs := make([]int64, 0, len(monitorIDs))
	seen := make(map[int64]struct{}, len(monitorIDs))
	for _, monitorID := range monitorIDs {
		if monitorID <= 0 {
			continue
		}
		if _, exists := seen[monitorID]; exists {
			continue
		}
		seen[monitorID] = struct{}{}
		uniqueMonitorIDs = append(uniqueMonitorIDs, monitorID)
		counts[monitorID] = ChannelMonitorRunCount{}
	}
	if len(uniqueMonitorIDs) == 0 {
		return counts, nil
	}

	var rows []struct {
		MonitorID int64
		Status    string
		RunCount  int64
	}
	query := DB.Model(&ChannelMonitorRun{}).
		Select("monitor_id, status, count(*) AS run_count").
		Where("monitor_id IN ?", uniqueMonitorIDs)
	if since > 0 {
		query = query.Where("started_at >= ?", since)
	}
	if err := query.Group("monitor_id, status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		count := counts[row.MonitorID]
		count.Total += row.RunCount
		if row.Status == ChannelMonitorStatusOperational || row.Status == ChannelMonitorStatusDegraded {
			count.Available += row.RunCount
		}
		counts[row.MonitorID] = count
	}
	return counts, nil
}

func CountChannelMonitorRuns(monitorID int64, since int64) (total, available int64, err error) {
	counts, err := CountChannelMonitorRunsForMonitors([]int64{monitorID}, since)
	if err != nil {
		return 0, 0, err
	}
	count := counts[monitorID]
	return count.Total, count.Available, nil
}

func SaveChannelMonitorRun(monitor *ChannelMonitor, run *ChannelMonitorRun, attempts []*ChannelMonitorAttempt) error {
	if monitor == nil || run == nil {
		return errors.New("monitor and run are required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return saveChannelMonitorRun(tx, monitor, run, attempts)
	})
}

func saveChannelMonitorRun(tx *gorm.DB, monitor *ChannelMonitor, run *ChannelMonitorRun, attempts []*ChannelMonitorAttempt) error {
	run.Source = NormalizeChannelMonitorRunSource(run.Source)
	if run.FinishedAt <= 0 {
		run.FinishedAt = common.GetTimestamp()
	}
	if run.StartedAt <= 0 || run.StartedAt > run.FinishedAt {
		run.StartedAt = run.FinishedAt
	}

	// Re-read the monitor under the transaction lock. An active probe can run
	// concurrently with real traffic; without this read, a late active failure
	// could overwrite the successful passive state observed during the probe.
	var current ChannelMonitor
	if err := lockForUpdate(tx).Where("id = ?", monitor.ID).First(&current).Error; err != nil {
		return err
	}
	NormalizeChannelMonitor(&current)
	run.MonitorID = current.ID
	validAttempts := make([]*ChannelMonitorAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt != nil {
			validAttempts = append(validAttempts, attempt)
		}
	}
	run.AttemptCount = len(validAttempts)

	if err := tx.Create(run).Error; err != nil {
		return err
	}
	for _, attempt := range validAttempts {
		attempt.RunID = run.ID
	}
	if len(validAttempts) > 0 {
		if err := tx.Create(&validAttempts).Error; err != nil {
			return err
		}
	}
	updates := map[string]any{"updated_at": common.GetTimestamp()}
	if run.Source == ChannelMonitorRunSourceTraffic {
		// Ignore an out-of-order passive callback. The run is still retained for
		// audit/history, but it must not roll the current status backwards.
		if run.FinishedAt <= current.LastPassiveAt {
			if run.FinishedAt > current.LastTrafficAt {
				updates["last_traffic_at"] = run.FinishedAt
			}
			return tx.Model(&ChannelMonitor{}).Where("id = ?", current.ID).Updates(updates).Error
		}
		// A passive sample is the authoritative recovery signal when it finishes
		// in the same second as an active probe, so allow it to win an equal
		// timestamp tie. It still must not replace a newer active result.
		stateUpdateAllowed := run.FinishedAt >= current.LastRunAt
		if run.FinishedAt > current.LastRunAt {
			updates["last_run_at"] = run.FinishedAt
		}
		if stateUpdateAllowed {
			updates["last_status"] = run.Status
			updates["last_response_time_ms"] = run.ResponseTimeMs
			updates["last_channel_id"] = run.FinalChannelID
			updates["last_channel_name"] = run.FinalChannelName
			updates["last_priority"] = run.FinalPriority
			updates["last_error"] = run.Error
			updates["last_degraded"] = run.Degraded
		}
		if run.FinishedAt > current.LastTrafficAt {
			updates["last_traffic_at"] = run.FinishedAt
		}
		if run.FinishedAt > current.LastPassiveAt {
			updates["last_passive_at"] = run.FinishedAt
		}
		// Real traffic is an immediate recovery signal only when this sample is
		// at least as new as the state currently recorded by an active probe.
		// An older passive callback can arrive after a newer active failure and
		// must not cancel that failure's pending confirmation.
		if stateUpdateAllowed {
			updates["failure_confirm_at"] = 0
			updates["failure_confirmed"] = false
		}
		return tx.Model(&ChannelMonitor{}).Where("id = ?", current.ID).Updates(updates).Error
	}

	// Do not let a failed active probe that started before real traffic replace
	// the traffic-derived status. A successful active probe is still allowed to
	// confirm recovery and upgrade the status.
	activeFailure := run.Status == ChannelMonitorStatusFailed || run.Status == ChannelMonitorStatusError
	trafficObservedDuringRun := current.LastTrafficAt >= run.StartedAt
	// LastRunAt is the ordering marker shared by active and passive results.
	// Older callbacks may remain in history, but they must not overwrite the
	// state fields or failure-confirmation markers written by a newer result.
	stateUpdateAllowed := run.FinishedAt > current.LastRunAt
	if run.FinishedAt > current.LastRunAt {
		updates["last_run_at"] = run.FinishedAt
	}
	if stateUpdateAllowed && (!activeFailure || !trafficObservedDuringRun) {
		updates["last_status"] = run.Status
		updates["last_response_time_ms"] = run.ResponseTimeMs
		updates["last_channel_id"] = run.FinalChannelID
		updates["last_channel_name"] = run.FinalChannelName
		updates["last_priority"] = run.FinalPriority
		updates["last_error"] = run.Error
		updates["last_degraded"] = run.Degraded
	}

	if stateUpdateAllowed && activeFailure && !trafficObservedDuringRun {
		if current.FailureConfirmAt > 0 {
			// The confirmation probe failed too; the outage is now confirmed.
			updates["failure_confirm_at"] = int64(0)
			updates["failure_confirmed"] = true
		} else if current.FailureConfirmed {
			// The outage was already confirmed on an earlier cycle. Keep the
			// confirmed state and avoid scheduling a short probe every interval.
			updates["failure_confirm_at"] = int64(0)
			updates["failure_confirmed"] = true
		} else {
			updates["failure_confirm_at"] = run.FinishedAt + ChannelMonitorFailureConfirmDelaySeconds
			updates["failure_confirmed"] = false
		}
	} else if stateUpdateAllowed && (!activeFailure || trafficObservedDuringRun) {
		updates["failure_confirm_at"] = int64(0)
		updates["failure_confirmed"] = false
	}

	// Keep the in-memory value useful to callers that reuse the monitor after a
	// run. The database update above remains the source of truth for others.
	*monitor = current
	if value, ok := updates["last_run_at"].(int64); ok {
		monitor.LastRunAt = value
	}
	if value, ok := updates["last_status"].(string); ok {
		monitor.LastStatus = value
	}
	if value, ok := updates["failure_confirm_at"].(int64); ok {
		monitor.FailureConfirmAt = value
	}
	if value, ok := updates["failure_confirmed"].(bool); ok {
		monitor.FailureConfirmed = value
	}
	return tx.Model(&ChannelMonitor{}).Where("id = ?", monitor.ID).Updates(updates).Error
}
