package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority          *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan           *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag" gorm:"index"`
	Setting           *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride     *string `json:"param_override" gorm:"type:text"`
	HeaderOverride    *string `json:"header_override" gorm:"type:text"`
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// cache info
	Keys []string `json:"-" gorm:"-"`
}

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
}

type ChannelSortOptions struct {
	SortBy    string
	SortOrder string
	IDSort    bool
}

var channelSortColumns = map[string]string{
	"id":            "id",
	"name":          "name",
	"priority":      "priority",
	"balance":       "balance",
	"response_time": "response_time",
	"test_time":     "test_time",
}

func NewChannelSortOptions(sortBy string, sortOrder string, idSort bool) ChannelSortOptions {
	normalizedSortBy := strings.ToLower(strings.TrimSpace(sortBy))
	normalizedSortOrder := strings.ToLower(strings.TrimSpace(sortOrder))
	if _, ok := channelSortColumns[normalizedSortBy]; !ok {
		normalizedSortBy = ""
		normalizedSortOrder = ""
	} else if normalizedSortOrder != "asc" {
		normalizedSortOrder = "desc"
	}

	return ChannelSortOptions{
		SortBy:    normalizedSortBy,
		SortOrder: normalizedSortOrder,
		IDSort:    idSort,
	}
}

func (options ChannelSortOptions) Apply(query *gorm.DB) *gorm.DB {
	if columnName, ok := channelSortColumns[options.SortBy]; ok {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: columnName},
			Desc:   options.SortOrder != "asc",
		})
	}
	if options.IDSort {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   true,
		})
	}
	return query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: "priority"},
		Desc:   true,
	})
}

func resolveChannelSortOptions(idSort bool, sortOptions []ChannelSortOptions) ChannelSortOptions {
	if len(sortOptions) == 0 {
		return NewChannelSortOptions("", "", idSort)
	}
	options := sortOptions[0]
	options.IDSort = options.IDSort || idSort
	return options
}

func NormalizeChannelGroupFilter(group string) string {
	group = strings.TrimSpace(group)
	if group == "" || strings.EqualFold(group, "all") || strings.EqualFold(group, "null") {
		return ""
	}
	return group
}

func channelGroupFilterCondition() string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ? ESCAPE '!'`
	}
	return `(',' || ` + commonGroupCol + ` || ',') LIKE ? ESCAPE '!'`
}

func channelGroupFilterPattern(group string) string {
	group = strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(group)
	return "%," + group + ",%"
}

func ApplyChannelGroupFilter(query *gorm.DB, group string) *gorm.DB {
	group = NormalizeChannelGroupFilter(group)
	if group == "" {
		return query
	}
	return query.Where(channelGroupFilterCondition(), channelGroupFilterPattern(group))
}

// Value implements driver.Valuer interface
func (c ChannelInfo) Value() (driver.Value, error) {
	return common.Marshal(&c)
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value interface{}) error {
	bytesValue, _ := value.([]byte)
	return common.Unmarshal(bytesValue, c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	return parseChannelKeys(channel.Key)
}

func parseChannelKeys(keyText string) []string {
	trimmed := strings.TrimSpace(keyText)
	if trimmed == "" {
		return []string{}
	}
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			keys := make([]string, 0, len(arr))
			for _, value := range arr {
				key := strings.TrimSpace(string(value))
				var stringKey string
				if err := common.Unmarshal(value, &stringKey); err == nil {
					key = strings.TrimSpace(stringKey)
				}
				if key != "" {
					keys = append(keys, key)
				}
			}
			return keys
		}
	}
	// Otherwise, fall back to splitting by newline
	rawKeys := strings.Split(keyText, "\n")
	keys := make([]string, 0, len(rawKeys))
	for _, key := range rawKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func (channel *Channel) GetNextEnabledKey() (key string, index int, apiErr *types.NewAPIError) {
	if channel == nil {
		return "", 0, types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	if !common.MemoryCacheEnabled && channel.Id > 0 {
		var latest Channel
		err := DB.Transaction(func(tx *gorm.DB) error {
			// Lock selection and cursor persistence in one transaction so separate
			// application instances cannot select the same polling slot.
			if err := lockForUpdate(tx).Select("id", "key", "channel_info").First(&latest, "id = ?", channel.Id).Error; err != nil {
				apiErr = types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
				return err
			}
			if !latest.ChannelInfo.IsMultiKey {
				key, index, apiErr = latest.Key, 0, nil
				channel.Key = latest.Key
				channel.ChannelInfo = latest.ChannelInfo
				return nil
			}
			key, index, apiErr = latest.selectNextEnabledKey()
			if apiErr != nil || latest.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
				return nil
			}
			if err := tx.Model(&Channel{}).Where("id = ?", latest.Id).Update("channel_info", latest.ChannelInfo).Error; err != nil {
				apiErr = types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
				return err
			}
			channel.ChannelInfo = latest.ChannelInfo
			channel.Key = latest.Key
			return nil
		})
		if err != nil && apiErr == nil {
			apiErr = types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		if apiErr != nil {
			return "", 0, apiErr
		}
		return key, index, nil
	}

	// Cached channel objects are shared by all requests. Keep the cache read and
	// cursor mutation under the cache lock so InitChannelCache cannot read a
	// partially updated ChannelInfo while it is carrying the cursor into a new
	// snapshot. Use the current cached pointer as the receiver as a refresh may
	// have replaced the object after the caller selected it.
	if common.MemoryCacheEnabled {
		channelSyncLock.Lock()
		defer channelSyncLock.Unlock()
		cached, ok := channelsIDM[channel.Id]
		if !ok {
			return "", 0, types.NewError(
				fmt.Errorf("渠道# %d，已不存在", channel.Id),
				types.ErrorCodeGetChannelFailed,
				types.ErrOptionWithSkipRetry(),
			)
		}
		channel = cached
	}
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}
	return channel.selectNextEnabledKey()
}

func (channel *Channel) selectNextEnabledKey() (key string, index int, apiErr *types.NewAPIError) {
	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		if common.DebugEnabled {
			logger.LogDebug(nil, "channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex)
		}
		// Start from the saved polling index and look for the next enabled key
		start := channel.ChannelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

func (channel *Channel) SaveChannelInfo() error {
	if channel.Id <= 0 {
		return errors.New("channel ID must be positive")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var latest Channel
		if err := lockForUpdate(tx).Select("id", "channel_info").First(&latest, "id = ?", channel.Id).Error; err != nil {
			return err
		}
		latest.ChannelInfo.MultiKeyPollingIndex = channel.ChannelInfo.MultiKeyPollingIndex
		if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).
			Update("channel_info", latest.ChannelInfo).Error; err != nil {
			return err
		}
		channel.ChannelInfo = latest.ChannelInfo
		return nil
	})
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]interface{} {
	otherInfo := make(map[string]interface{})
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]interface{}) {
	otherInfoBytes, err := common.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return DB.Save(channel).Error
}

// saveStatusState persists only the fields owned by the channel status flow.
// Keeping this allowlist here prevents a stale channel snapshot from
// overwriting credentials, accounting counters, or channel configuration.
func (channel *Channel) saveStatusState() error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	updates := map[string]any{
		"status":     channel.Status,
		"other_info": channel.OtherInfo,
	}
	if channel.ChannelInfo.IsMultiKey {
		updates["channel_info"] = channel.ChannelInfo
	}
	return DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(updates).Error
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := resolveChannelSortOptions(idSort, sortOptions)
	if selectAll {
		err = order.Apply(DB).Find(&channels).Error
	} else {
		err = order.Apply(DB).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	order := resolveChannelSortOptions(idSort, sortOptions)
	query := order.Apply(DB.Where("tag = ?", tag))
	if !selectAll {
		query = query.Omit("key")
	}
	err := query.Find(&channels).Error
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := resolveChannelSortOptions(idSort, sortOptions)

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	// 执行查询
	err := order.Apply(baseQuery).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) (err error) {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			err = fmt.Errorf("batch insert channels panicked: %v", r)
		}
	}()

	for _, chunk := range lo.Chunk(channels, 50) {
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range chunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var deletedCount int64
	for _, chunk := range lo.Chunk(ids, 200) {
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		result := tx.Where("id in (?)", chunk).Delete(&Channel{})
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		deletedCount += result.RowsAffected
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return deletedCount, nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return channelWeightFromUint(*channel.Weight)
}

func channelWeightFromUint(weight uint) int {
	maxInt := int(^uint(0) >> 1)
	if weight > uint(maxInt) {
		return maxInt
	}
	return int(weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.ChannelBaseURLs[channel.Type]
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(channel).Error; err != nil {
			return err
		}
		return channel.AddAbilities(tx)
	})
}

func (channel *Channel) Update() error {
	return channel.update(nil)
}

// UpdateSelected persists only the explicitly selected channel columns. It is
// used by PATCH-like API handlers so valid zero values (for example an empty
// provider-specific setting) are written without clearing
// omitted fields or server-owned accounting data.
func (channel *Channel) UpdateSelected(columns []string) error {
	if columns == nil {
		columns = []string{}
	}
	return channel.update(columns)
}

func (channel *Channel) update(columns []string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return channel.updateSelectedInTransaction(tx, columns)
	})
}

func (channel *Channel) updateSelectedInTransaction(tx *gorm.DB, columns []string) error {
	if columns != nil && len(columns) == 0 {
		return tx.First(channel, "id = ?", channel.Id).Error
	}
	refreshAbilities := columns == nil
	if !refreshAbilities {
		for _, column := range columns {
			switch column {
			case "models", "group", "status", "priority", "weight", "tag":
				refreshAbilities = true
			}
		}
	}

	// If this is a multi-key channel, recalculate MultiKeySize based on the
	// current key list to avoid inconsistency after editing keys.
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		keyProvided := strings.TrimSpace(channel.Key) != ""
		if keyProvided {
			keyStr = channel.Key
		} else {
			// A blank value means the caller did not rotate the secret. Read the
			// key under the same transaction and propagate lookup failures instead
			// of silently persisting a zero-sized key pool.
			channel.Key = ""
			var existing Channel
			if err := lockForUpdate(tx).Select("key").First(&existing, "id = ?", channel.Id).Error; err != nil {
				return err
			}
			keyStr = existing.Key
		}
		keys := parseChannelKeys(keyStr)
		if keyProvided {
			channel.Key = strings.Join(keys, "\n")
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		// Clean up all per-key state that no longer points at a real key.
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx < 0 || idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
		if channel.ChannelInfo.MultiKeyDisabledReason != nil {
			for idx := range channel.ChannelInfo.MultiKeyDisabledReason {
				if idx < 0 || idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyDisabledReason, idx)
				}
			}
		}
		if channel.ChannelInfo.MultiKeyDisabledTime != nil {
			for idx := range channel.ChannelInfo.MultiKeyDisabledTime {
				if idx < 0 || idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyDisabledTime, idx)
				}
			}
		}
		if channel.ChannelInfo.MultiKeyPollingIndex < 0 ||
			channel.ChannelInfo.MultiKeyPollingIndex >= channel.ChannelInfo.MultiKeySize {
			channel.ChannelInfo.MultiKeyPollingIndex = 0
		}
	}

	query := tx.Model(&Channel{}).Where("id = ?", channel.Id)
	if columns != nil {
		query = query.Select(columns)
	}
	if err := query.Updates(channel).Error; err != nil {
		return err
	}
	if err := tx.First(channel, "id = ?", channel.Id).Error; err != nil {
		return err
	}
	if !refreshAbilities {
		return nil
	}
	return channel.UpdateAbilities(tx)
}

// MutateChannelSelected reloads and locks a channel before applying a
// read-modify-write update. The database row lock protects JSON-backed channel
// state from lost updates across application instances.
func MutateChannelSelected(id int, mutate func(*Channel) ([]string, error)) (*Channel, error) {
	if id <= 0 {
		return nil, errors.New("channel ID must be positive")
	}
	if mutate == nil {
		return nil, errors.New("channel mutation is required")
	}
	channel := &Channel{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(channel, "id = ?", id).Error; err != nil {
			return err
		}
		columns, err := mutate(channel)
		if err != nil {
			return err
		}
		return channel.updateSelectedInTransaction(tx, columns)
	})
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
			return err
		}
		return tx.Delete(channel).Error
	})
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	if err := DB.Model(&Channel{}).Pluck("id", &activeChannelIds).Error; err != nil {
		// A failed snapshot is not the same as an empty channel table. Keeping
		// the existing locks is safe; deleting them here could give one channel
		// two different mutexes while a polling operation is still in flight.
		common.SysError("failed to list channels while cleaning polling locks: " + err.Error())
		return
	}

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) bool {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		if channel.Status == status {
			return false
		}
		channel.Status = status
		return true
	}

	keyIndex := -1
	for index, key := range keys {
		if key == usingKey {
			keyIndex = index
			break
		}
	}
	if keyIndex < 0 {
		if usingKey != "" {
			common.SysLog(fmt.Sprintf("failed to update multi-key status: channel_id=%d, using key not found", channel.Id))
			return false
		}
		if channel.Status == status {
			return false
		}
		channel.Status = status
		info := channel.GetOtherInfo()
		info["status_reason"] = reason
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
		return true
	}

	currentKeyStatus := common.ChannelStatusEnabled
	if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; exists {
		currentKeyStatus = storedStatus
	}
	beforeChannelStatus := channel.Status
	if channel.ChannelInfo.MultiKeyStatusList == nil {
		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
	}
	if status == common.ChannelStatusEnabled {
		delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
		delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
	} else {
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
		channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
		channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
	}

	if !hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
		// Per-key health changes must never override an administrator's manual
		// channel disable. Otherwise re-enabling one credential later would also
		// re-enable the whole channel without an explicit admin action.
		if beforeChannelStatus == common.ChannelStatusEnabled || beforeChannelStatus == common.ChannelStatusAutoDisabled {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = "All keys are disabled"
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		}
	} else if status == common.ChannelStatusEnabled && beforeChannelStatus == common.ChannelStatusAutoDisabled {
		channel.Status = common.ChannelStatusEnabled
		info := channel.GetOtherInfo()
		delete(info, "status_reason")
		delete(info, "status_time")
		channel.SetOtherInfo(info)
	}
	return currentKeyStatus != status || beforeChannelStatus != channel.Status
}
func hasEnabledMultiKey(keys []string, statusList map[int]int) bool {
	for i := range keys {
		if statusList == nil {
			return true
		}
		status, ok := statusList[i]
		if !ok || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

func updateChannelStatus(channelId int, usingKey string, status int, reason string) (*Channel, bool, bool, error) {
	// Keep key selection and status changes ordered inside this process. The
	// transaction row lock below provides the same lost-update protection across
	// application instances.
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	changed := false
	statusChanged := false
	updated, err := MutateChannelSelected(channelId, func(channel *Channel) ([]string, error) {
		beforeStatus := channel.Status
		if channel.ChannelInfo.IsMultiKey {
			changed = handlerMultiKeyUpdate(channel, usingKey, status, reason)
		} else {
			if channel.Status == status {
				return []string{}, nil
			}
			channel.Status = status
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			changed = true
		}
		if !changed {
			return []string{}, nil
		}

		statusChanged = beforeStatus != channel.Status
		columns := make([]string, 0, 3)
		if statusChanged {
			columns = append(columns, "status", "other_info")
		}
		if channel.ChannelInfo.IsMultiKey && usingKey != "" {
			columns = append(columns, "channel_info")
		}
		return columns, nil
	})
	if err != nil {
		return nil, false, false, err
	}
	if !changed {
		return updated, false, false, nil
	}
	return updated, true, statusChanged, nil
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}

	updated, changed, statusChanged, err := updateChannelStatus(channelId, usingKey, status, reason)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channelId, status, err))
		return false
	}
	if !changed {
		return false
	}
	if common.MemoryCacheEnabled {
		if statusChanged {
			InitChannelCache()
		} else {
			CacheUpdateChannel(updated)
		}
	}
	return true
}

// BatchUpdateChannelStatus applies administrative status changes and refreshes
// the routing cache once after the batch instead of reloading every channel and
// ability row after each individual update.
func BatchUpdateChannelStatus(channelIds []int, status int, reason string) int {
	if len(channelIds) == 0 {
		return 0
	}
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}

	changedCount := 0
	statusChanged := false
	updatedChannels := make([]*Channel, 0, len(channelIds))
	seen := make(map[int]struct{}, len(channelIds))
	for _, channelId := range channelIds {
		if _, exists := seen[channelId]; exists {
			continue
		}
		seen[channelId] = struct{}{}
		updated, changed, changedStatus, err := updateChannelStatus(channelId, "", status, reason)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channelId, status, err))
			continue
		}
		if !changed {
			continue
		}
		changedCount++
		statusChanged = statusChanged || changedStatus
		updatedChannels = append(updatedChannels, updated)
	}

	if !common.MemoryCacheEnabled || changedCount == 0 {
		return changedCount
	}
	if statusChanged {
		InitChannelCache()
		return changedCount
	}
	for _, channel := range updatedChannels {
		CacheUpdateChannel(channel)
	}
	return changedCount
}
func updateChannelStatusByTag(tag string, status int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channelIds []int
		if err := lockForUpdate(tx).Model(&Channel{}).Where("tag = ?", tag).Pluck("id", &channelIds).Error; err != nil {
			return err
		}
		if len(channelIds) == 0 {
			return nil
		}
		if err := tx.Model(&Channel{}).Where("id IN ?", channelIds).Update("status", status).Error; err != nil {
			return err
		}
		return tx.Model(&Ability{}).Where("channel_id IN ?", channelIds).
			Update("enabled", status == common.ChannelStatusEnabled).Error
	})
}

func EnableChannelByTag(tag string) error {
	return updateChannelStatusByTag(tag, common.ChannelStatusEnabled)
}

func DisableChannelByTag(tag string) error {
	return updateChannelStatusByTag(tag, common.ChannelStatusManuallyDisabled)
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var channels []*Channel
		if err := lockForUpdate(tx).Where("tag = ?", tag).Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) == 0 {
			return nil
		}
		channelIds := make([]int, 0, len(channels))
		for _, channel := range channels {
			channelIds = append(channelIds, channel.Id)
		}
		if err := tx.Model(&Channel{}).Where("id IN ?", channelIds).Updates(updateData).Error; err != nil {
			return err
		}
		if shouldReCreateAbilities {
			channels = nil
			if err := tx.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
				return err
			}
			for _, channel := range channels {
				if err := channel.UpdateAbilities(tx); err != nil {
					return err
				}
			}
			return nil
		}

		abilityUpdates := make(map[string]any)
		if newTag != nil && *newTag != tag {
			abilityUpdates["tag"] = *newTag
		}
		if priority != nil {
			abilityUpdates["priority"] = *priority
		}
		if weight != nil {
			abilityUpdates["weight"] = *weight
		}
		if len(abilityUpdates) == 0 {
			return nil
		}
		return tx.Model(&Ability{}).Where("channel_id IN ?", channelIds).Updates(abilityUpdates).Error
	})
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	return deleteChannelsByStatuses([]int64{status})
}

func DeleteDisabledChannel() (int64, error) {
	return deleteChannelsByStatuses([]int64{common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled})
}

func deleteChannelsByStatuses(statuses []int64) (deletedCount int64, err error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var ids []int
		if err := lockForUpdate(tx).Model(&Channel{}).
			Where("status IN ?", statuses).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		for _, chunk := range lo.Chunk(ids, 200) {
			if err := tx.Where("channel_id IN ?", chunk).Delete(&Ability{}).Error; err != nil {
				return err
			}
			result := tx.Where("id IN ?", chunk).Delete(&Channel{})
			if result.Error != nil {
				return result.Error
			}
			deletedCount += result.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deletedCount, nil
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	return GetPaginatedChannelTags(DB.Model(&Channel{}), offset, limit)
}

func GetPaginatedChannelTags(query *gorm.DB, offset int, limit int) ([]*string, error) {
	var tags []*string
	err := query.
		Select("DISTINCT tag").
		Where("tag is not null AND tag != ''").
		Order(clause.OrderByColumn{Column: clause.Column{Name: "tag"}}).
		Offset(offset).
		Limit(limit).
		Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	subQuery := baseQuery.
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return err
		}
	}
	if _, err := common.ParseProxyURLStrict(channelParams.Proxy); err != nil {
		return fmt.Errorf("invalid channel proxy: %w", err)
	}
	if err := channelParams.ValidateHTTPTransport(); err != nil {
		return err
	}
	channelOtherSettings := &dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, channelOtherSettings)
		if err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if channelOtherSettings.AdvancedCustom == nil {
			return fmt.Errorf("advanced_custom is required")
		}
	}
	if channelOtherSettings.AdvancedCustom != nil {
		if err := channelOtherSettings.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom && channelOtherSettings.UpstreamModelUpdateCheckEnabled {
		if _, ok := channelOtherSettings.AdvancedCustom.ModelListRoute(); !ok {
			return fmt.Errorf("advanced custom channels require a %s route when upstream model update checks are enabled", dto.AdvancedCustomModelListPath)
		}
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Channel{}).Where("id IN ?", ids).Update("tag", tag).Error; err != nil {
			return err
		}
		return tx.Model(&Ability{}).Where("channel_id IN ?", ids).Update("tag", tag).Error
	})
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	return CountChannelTags(DB.Model(&Channel{}))
}

func CountChannelTags(query *gorm.DB) (int64, error) {
	var total int64
	err := query.Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
