package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type OpenAIModel struct {
	ID         string         `json:"id"`
	Object     string         `json:"object"`
	Created    int64          `json:"created"`
	OwnedBy    string         `json:"owned_by"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Permission []struct {
		ID                 string `json:"id"`
		Object             string `json:"object"`
		Created            int64  `json:"created"`
		AllowCreateEngine  bool   `json:"allow_create_engine"`
		AllowSampling      bool   `json:"allow_sampling"`
		AllowLogprobs      bool   `json:"allow_logprobs"`
		AllowSearchIndices bool   `json:"allow_search_indices"`
		AllowView          bool   `json:"allow_view"`
		AllowFineTuning    bool   `json:"allow_fine_tuning"`
		Organization       string `json:"organization"`
		Group              string `json:"group"`
		IsBlocking         bool   `json:"is_blocking"`
	} `json:"permission"`
	Root   string `json:"root"`
	Parent string `json:"parent"`
}

type OpenAIModelsResponse struct {
	Data    []OpenAIModel `json:"data"`
	Success bool          `json:"success"`
}

func parseStatusFilter(statusParam string) int {
	switch strings.ToLower(statusParam) {
	case "enabled", "1":
		return common.ChannelStatusEnabled
	case "disabled", "0":
		return 0
	default:
		return -1
	}
}

func clearChannelInfo(channel *model.Channel) {
	if channel.ChannelInfo.IsMultiKey {
		channel.ChannelInfo.MultiKeyDisabledReason = nil
		channel.ChannelInfo.MultiKeyDisabledTime = nil
	}
}

func applyChannelStatusFilter(query *gorm.DB, statusFilter int) *gorm.DB {
	if statusFilter == common.ChannelStatusEnabled {
		return query.Where("status = ?", common.ChannelStatusEnabled)
	}
	if statusFilter == 0 {
		return query.Where("status != ?", common.ChannelStatusEnabled)
	}
	return query
}

func buildChannelListQuery(group string, statusFilter int, typeFilter int) *gorm.DB {
	query := model.DB.Model(&model.Channel{})
	query = model.ApplyChannelGroupFilter(query, group)
	query = applyChannelStatusFilter(query, statusFilter)
	if typeFilter >= 0 {
		query = query.Where("type = ?", typeFilter)
	}
	return query
}

func GetChannelOps(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"retry_times": common.RetryTimes,
	})
}

func GetAllChannels(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelData := make([]*model.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	sortOptions := model.NewChannelSortOptions(c.Query("sort_by"), c.Query("sort_order"), idSort)
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	groupFilter := model.NormalizeChannelGroupFilter(c.Query("group"))
	statusParam := c.Query("status")
	// statusFilter: -1 all, 1 enabled, 0 disabled (include auto & manual)
	statusFilter := parseStatusFilter(statusParam)
	// type filter
	typeStr := c.Query("type")
	typeFilter := -1
	if typeStr != "" {
		if t, err := strconv.Atoi(typeStr); err == nil {
			typeFilter = t
		}
	}

	var total int64

	if enableTagMode {
		tags, err := model.GetPaginatedChannelTags(buildChannelListQuery(groupFilter, statusFilter, typeFilter), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
		if err != nil {
			common.SysError("failed to get paginated tags: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取标签失败，请稍后重试"})
			return
		}
		total, err = model.CountChannelTags(buildChannelListQuery(groupFilter, statusFilter, typeFilter))
		if err != nil {
			common.SysError("failed to count tags: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取标签数量失败，请稍后重试"})
			return
		}
		for _, tag := range tags {
			if tag == nil || *tag == "" {
				continue
			}
			var tagChannels []*model.Channel
			err := sortOptions.Apply(buildChannelListQuery(groupFilter, statusFilter, typeFilter).Where("tag = ?", *tag)).
				Omit("key").
				Find(&tagChannels).Error
			if err != nil {
				common.SysError("failed to get channels by tag: " + err.Error())
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取标签渠道失败，请稍后重试"})
				return
			}
			channelData = append(channelData, tagChannels...)
		}
	} else {
		if err := buildChannelListQuery(groupFilter, statusFilter, typeFilter).Count(&total).Error; err != nil {
			common.SysError("failed to count channels: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取渠道数量失败，请稍后重试"})
			return
		}

		err := sortOptions.Apply(buildChannelListQuery(groupFilter, statusFilter, typeFilter)).
			Limit(pageInfo.GetPageSize()).
			Offset(pageInfo.GetStartIdx()).
			Omit("key").
			Find(&channelData).Error
		if err != nil {
			common.SysError("failed to get channels: " + err.Error())
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取渠道列表失败，请稍后重试"})
			return
		}
	}

	for _, datum := range channelData {
		clearChannelInfo(datum)
	}

	countQuery := buildChannelListQuery(groupFilter, statusFilter, -1)
	var results []struct {
		Type  int64
		Count int64
	}
	if err := countQuery.Select("type, count(*) as count").Group("type").Find(&results).Error; err != nil {
		common.SysError("failed to count channel types: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取渠道类型统计失败，请稍后重试"})
		return
	}
	typeCounts := make(map[int64]int64)
	for _, r := range results {
		typeCounts[r.Type] = r.Count
	}
	common.ApiSuccess(c, gin.H{
		"items":       channelData,
		"total":       total,
		"page":        pageInfo.GetPage(),
		"page_size":   pageInfo.GetPageSize(),
		"type_counts": typeCounts,
	})
	return
}

func buildFetchModelsHeaders(channel *model.Channel, key string) (http.Header, error) {
	var headers http.Header
	switch channel.Type {
	case constant.ChannelTypeAnthropic:
		headers = GetClaudeAuthHeader(key)
	default:
		headers = GetAuthHeader(key)
	}

	if err := applyFetchModelsHeaderOverrides(channel, key, headers); err != nil {
		return nil, err
	}
	return headers, nil
}

func applyFetchModelsHeaderOverrides(channel *model.Channel, key string, headers http.Header) error {
	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:          key,
			HeadersOverride: channel.GetHeaderOverride(),
		},
	}
	overrides, err := relaychannel.ResolveHeaderOverride(info, nil)
	if err != nil {
		return err
	}
	for name, value := range overrides {
		headers.Set(name, value)
	}

	return nil
}

func FetchUpstreamModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	ids, err := fetchChannelUpstreamModelIDs(channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("获取模型列表失败: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    ids,
	})
}

func FixChannelsAbilities(c *gin.Context) {
	success, fails, err := model.FixAbility()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"success": success,
			"fails":   fails,
		},
	})
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelKeyword := c.Query("model")
	statusParam := c.Query("status")
	statusFilter := parseStatusFilter(statusParam)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	sortOptions := model.NewChannelSortOptions(c.Query("sort_by"), c.Query("sort_order"), idSort)
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	channelData := make([]*model.Channel, 0)
	if enableTagMode {
		tags, err := model.SearchTags(keyword, group, modelKeyword, idSort)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		for _, tag := range tags {
			if tag != nil && *tag != "" {
				var tagChannels []*model.Channel
				err := sortOptions.Apply(buildChannelListQuery(group, -1, -1).Where("tag = ?", *tag)).
					Omit("key").
					Find(&tagChannels).Error
				if err != nil {
					c.JSON(http.StatusOK, gin.H{
						"success": false,
						"message": err.Error(),
					})
					return
				}
				channelData = append(channelData, tagChannels...)
			}
		}
	} else {
		channels, err := model.SearchChannels(keyword, group, modelKeyword, idSort, sortOptions)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		channelData = channels
	}

	if statusFilter == common.ChannelStatusEnabled || statusFilter == 0 {
		filtered := make([]*model.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if statusFilter == common.ChannelStatusEnabled && ch.Status != common.ChannelStatusEnabled {
				continue
			}
			if statusFilter == 0 && ch.Status == common.ChannelStatusEnabled {
				continue
			}
			filtered = append(filtered, ch)
		}
		channelData = filtered
	}

	// calculate type counts for search results
	typeCounts := make(map[int64]int64)
	for _, channel := range channelData {
		typeCounts[int64(channel.Type)]++
	}

	typeParam := c.Query("type")
	typeFilter := -1
	if typeParam != "" {
		if tp, err := strconv.Atoi(typeParam); err == nil {
			typeFilter = tp
		}
	}

	if typeFilter >= 0 {
		filtered := make([]*model.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if ch.Type == typeFilter {
				filtered = append(filtered, ch)
			}
		}
		channelData = filtered
	}

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	total := len(channelData)
	startIdx := (page - 1) * pageSize
	if startIdx > total {
		startIdx = total
	}
	endIdx := startIdx + pageSize
	if endIdx > total {
		endIdx = total
	}

	pagedData := channelData[startIdx:endIdx]

	for _, datum := range pagedData {
		clearChannelInfo(datum)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":       pagedData,
			"total":       total,
			"type_counts": typeCounts,
		},
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channel != nil {
		clearChannelInfo(channel)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// GetChannelKey 获取渠道密钥（需要通过安全验证中间件）
// 此函数依赖 SecureVerificationRequired 中间件，确保用户已通过安全验证
func GetChannelKey(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("渠道ID格式错误: %v", err))
		return
	}

	// 获取渠道信息（包含密钥）
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, fmt.Errorf("获取渠道信息失败: %v", err))
		return
	}

	if channel == nil {
		common.ApiError(c, fmt.Errorf("渠道不存在"))
		return
	}

	// 记录操作审计日志（高危：查看渠道密钥）
	recordManageAudit(c, "channel.key_view", map[string]interface{}{
		"id":   channelId,
		"name": channel.Name,
	})

	// 返回渠道密钥
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取成功",
		"data": map[string]interface{}{
			"key": channel.Key,
		},
	})
}

// validateTwoFactorAuth 统一的2FA验证函数
func validateTwoFactorAuth(twoFA *model.TwoFA, code string) bool {
	// 尝试验证TOTP
	if cleanCode, err := common.ValidateNumericCode(code); err == nil {
		if isValid, _ := twoFA.ValidateTOTPAndUpdateUsage(cleanCode); isValid {
			return true
		}
	}

	// 尝试验证备用码
	if isValid, err := twoFA.ValidateBackupCodeAndUpdateUsage(code); err == nil && isValid {
		return true
	}

	return false
}

// validateChannel 通用的渠道校验函数
func validateChannel(channel *model.Channel, isAdd bool) error {
	if channel == nil {
		return fmt.Errorf("channel cannot be empty")
	}
	if isAdd {
		if err := normalizeChannelRoutingFields(channel, true, nil); err != nil {
			return err
		}
	}

	// 校验 channel settings
	if err := channel.ValidateSettings(); err != nil {
		return fmt.Errorf("渠道额外设置[channel setting] 格式错误：%s", err.Error())
	}

	if channel.Type == constant.ChannelTypeNewAPI && strings.TrimSpace(channel.GetBaseURL()) == "" {
		return fmt.Errorf("New API channel base URL cannot be empty")
	}

	// 如果是添加操作，检查 channel 和 key 是否为空
	if isAdd {
		if _, ok := constant.ChannelTypeNames[channel.Type]; !ok || channel.Type == constant.ChannelTypeUnknown {
			return fmt.Errorf("channel type is invalid")
		}
		if strings.TrimSpace(channel.Key) == "" {
			return fmt.Errorf("channel cannot be empty")
		}

	}
	if err := validateChannelRoutingLengths(channel); err != nil {
		return err
	}
	if channel.ChannelInfo.IsMultiKey {
		if channel.Type == constant.ChannelTypeCodex {
			return errors.New("Codex 渠道不支持多密钥")
		}
		if channel.Type == constant.ChannelTypeVertexAi &&
			channel.GetOtherSettings().VertexKeyType == dto.VertexKeyTypeAPIKey {
			return errors.New("Vertex AI API Key 模式不支持多密钥")
		}
	}

	// VertexAI 特殊校验
	if channel.Type == constant.ChannelTypeVertexAi {
		if channel.Other == "" {
			return fmt.Errorf("部署地区不能为空")
		}

		regionMap, err := common.StrToMap(channel.Other)
		if err != nil {
			return fmt.Errorf("部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}")
		}

		if regionMap["default"] == nil {
			return fmt.Errorf("部署地区必须包含default字段")
		}
	}

	// Codex OAuth key validation (optional, only when JSON object is provided)
	if channel.Type == constant.ChannelTypeCodex {
		trimmedKey := strings.TrimSpace(channel.Key)
		if isAdd || trimmedKey != "" {
			if !strings.HasPrefix(trimmedKey, "{") {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			var keyMap map[string]any
			if err := common.Unmarshal([]byte(trimmedKey), &keyMap); err != nil {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			if v, ok := keyMap["access_token"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include access_token")
			}
			if v, ok := keyMap["account_id"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include account_id")
			}
		}
	}

	return nil
}

func validateChannelRoutingLengths(channel *model.Channel) error {
	if utf8.RuneCountInString(channel.Name) > 255 {
		return errors.New("渠道名称过长，最多 255 个字符")
	}
	for _, modelName := range channel.GetModels() {
		if utf8.RuneCountInString(modelName) > 255 {
			return fmt.Errorf("模型名称过长: %s", modelName)
		}
	}
	if utf8.RuneCountInString(channel.Group) > 64 {
		return errors.New("渠道分组过长，最多 64 个字符")
	}
	return nil
}

func normalizeCommaSeparatedChannelValues(value string) string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		values = append(values, item)
	}
	return strings.Join(values, ",")
}

func normalizeChannelRoutingFields(channel *model.Channel, isAdd bool, requestData map[string]any) error {
	validateName := isAdd
	validateModels := isAdd
	validateGroup := isAdd
	if requestData != nil {
		_, validateName = requestData["name"]
		_, validateModels = requestData["models"]
		_, validateGroup = requestData["group"]
	}
	if validateName {
		channel.Name = strings.TrimSpace(channel.Name)
		if channel.Name == "" {
			return errors.New("channel name cannot be empty")
		}
	}
	if validateModels {
		channel.Models = normalizeCommaSeparatedChannelValues(channel.Models)
		if channel.Models == "" {
			return errors.New("channel models cannot be empty")
		}
	}
	if validateGroup {
		channel.Group = normalizeCommaSeparatedChannelValues(channel.Group)
		if channel.Group == "" {
			return errors.New("channel group cannot be empty")
		}
	}
	return nil
}

func RefreshCodexChannelCredential(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	oauthKey, ch, err := service.RefreshCodexChannelCredential(ctx, channelId, service.CodexCredentialRefreshOptions{ResetCaches: true})
	if err != nil {
		common.SysError("failed to refresh codex channel credential: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "刷新凭证失败，请稍后重试"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "refreshed",
		"data": gin.H{
			"expires_at":   oauthKey.Expired,
			"last_refresh": oauthKey.LastRefresh,
			"account_id":   oauthKey.AccountID,
			"email":        oauthKey.Email,
			"channel_id":   ch.Id,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
		},
	})
}

type AddChannelRequest struct {
	Mode                      string                `json:"mode"`
	MultiKeyMode              constant.MultiKeyMode `json:"multi_key_mode"`
	BatchAddSetKeyPrefix2Name bool                  `json:"batch_add_set_key_prefix_2_name"`
	Channel                   *model.Channel        `json:"channel"`
}

func getVertexArrayKeys(keys string) ([]string, error) {
	keys = strings.TrimSpace(keys)
	if keys == "" {
		return nil, fmt.Errorf("批量添加 Vertex AI 的 keys 不能为空")
	}
	var keyArray []json.RawMessage
	err := common.Unmarshal([]byte(keys), &keyArray)
	if err != nil {
		return nil, fmt.Errorf("批量添加 Vertex AI 必须使用标准的JsonArray格式，例如[{key1}, {key2}...]，请检查输入: %w", err)
	}
	cleanKeys := make([]string, 0, len(keyArray))
	for index, key := range keyArray {
		keyStr, err := normalizeVertexCredentialJSON(key)
		if err != nil {
			return nil, fmt.Errorf("Vertex AI key #%d 格式错误: %w", index+1, err)
		}
		cleanKeys = append(cleanKeys, keyStr)
	}
	if len(cleanKeys) == 0 {
		return nil, fmt.Errorf("批量添加 Vertex AI 的 keys 不能为空")
	}
	return cleanKeys, nil
}

func normalizeVertexCredentialJSON(raw []byte) (string, error) {
	var credential map[string]json.RawMessage
	if err := common.Unmarshal(raw, &credential); err != nil {
		// Older clients sometimes wrapped each service-account object in a JSON
		// string before putting it in the outer array. Accept that representation
		// only when the decoded string is itself a JSON object.
		var encodedCredential string
		if stringErr := common.Unmarshal(raw, &encodedCredential); stringErr != nil {
			return "", errors.New("服务账号密钥必须是 JSON 对象")
		}
		if err := common.Unmarshal([]byte(strings.TrimSpace(encodedCredential)), &credential); err != nil {
			return "", errors.New("服务账号密钥必须是 JSON 对象")
		}
	}
	if credential == nil {
		return "", errors.New("服务账号密钥必须是 JSON 对象")
	}
	encoded, err := common.Marshal(credential)
	if err != nil {
		return "", fmt.Errorf("Vertex AI key JSON 编码失败: %w", err)
	}
	return string(encoded), nil
}

func splitChannelKeys(keys string) []string {
	rawKeys := strings.Split(keys, "\n")
	cleanKeys := make([]string, 0, len(rawKeys))
	for _, key := range rawKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleanKeys = append(cleanKeys, key)
	}
	return cleanKeys
}

func isValidMultiKeyMode(mode constant.MultiKeyMode) bool {
	return mode == constant.MultiKeyModeRandom || mode == constant.MultiKeyModePolling
}

func AddChannel(c *gin.Context) {
	addChannelRequest := AddChannelRequest{}
	err := c.ShouldBindJSON(&addChannelRequest)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if addChannelRequest.Channel == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "channel cannot be empty",
		})
		return
	}

	// Creation never accepts database/accounting state from the client. Clear
	// it before validation as well: an older client may echo stale ChannelInfo
	// from a previous edit into an otherwise valid single-key create request.
	addChannelRequest.Channel.Id = 0
	addChannelRequest.Channel.TestTime = 0
	addChannelRequest.Channel.ResponseTime = 0
	addChannelRequest.Channel.Balance = 0
	addChannelRequest.Channel.BalanceUpdatedTime = 0
	addChannelRequest.Channel.UsedQuota = 0
	addChannelRequest.Channel.ChannelInfo = model.ChannelInfo{}
	addChannelRequest.Channel.Keys = nil

	// 使用统一的校验函数
	if err := validateChannel(addChannelRequest.Channel, true); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if addChannelRequest.Channel.Status == common.ChannelStatusUnknown {
		addChannelRequest.Channel.Status = common.ChannelStatusEnabled
	}
	if !isManageableChannelStatus(addChannelRequest.Channel.Status) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "不支持的渠道状态",
		})
		return
	}
	if addChannelRequest.Channel.Type == constant.ChannelTypeCodex && addChannelRequest.Mode != "single" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Codex 渠道不支持批量或多密钥创建",
		})
		return
	}
	if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi &&
		addChannelRequest.Channel.GetOtherSettings().VertexKeyType == dto.VertexKeyTypeAPIKey &&
		addChannelRequest.Mode != "single" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Vertex AI API Key 模式不支持批量或多密钥创建",
		})
		return
	}
	addChannelRequest.Channel.CreatedTime = common.GetTimestamp()
	keys := make([]string, 0)
	switch addChannelRequest.Mode {
	case "multi_to_single":
		if addChannelRequest.MultiKeyMode == "" {
			addChannelRequest.MultiKeyMode = constant.MultiKeyModeRandom
		}
		if !isValidMultiKeyMode(addChannelRequest.MultiKeyMode) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "不支持的多密钥轮询模式",
			})
			return
		}
		addChannelRequest.Channel.ChannelInfo.IsMultiKey = true
		addChannelRequest.Channel.ChannelInfo.MultiKeyMode = addChannelRequest.MultiKeyMode
		if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi && addChannelRequest.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			array, err := getVertexArrayKeys(addChannelRequest.Channel.Key)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			addChannelRequest.Channel.ChannelInfo.MultiKeySize = len(array)
			addChannelRequest.Channel.Key = strings.Join(array, "\n")
		} else {
			cleanKeys := splitChannelKeys(addChannelRequest.Channel.Key)
			addChannelRequest.Channel.ChannelInfo.MultiKeySize = len(cleanKeys)
			addChannelRequest.Channel.Key = strings.Join(cleanKeys, "\n")
		}
		keys = []string{addChannelRequest.Channel.Key}
	case "batch":
		if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi && addChannelRequest.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			// multi json
			keys, err = getVertexArrayKeys(addChannelRequest.Channel.Key)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		} else {
			keys = splitChannelKeys(addChannelRequest.Channel.Key)
		}
	case "single":
		key := addChannelRequest.Channel.Key
		if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi && addChannelRequest.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			key, err = normalizeVertexCredentialJSON([]byte(strings.TrimSpace(key)))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Vertex AI key 格式错误: " + err.Error(),
				})
				return
			}
		}
		keys = []string{key}
	default:
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "不支持的添加模式",
		})
		return
	}

	channels := make([]model.Channel, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		localChannel := *addChannelRequest.Channel
		localChannel.Key = key
		if addChannelRequest.BatchAddSetKeyPrefix2Name && len(keys) > 1 {
			keyPrefix := localChannel.Key
			keyRunes := []rune(localChannel.Key)
			if len(keyRunes) > 8 {
				keyPrefix = string(keyRunes[:8])
			}
			localChannel.Name = fmt.Sprintf("%s %s", localChannel.Name, keyPrefix)
		}
		if err := validateChannelRoutingLengths(&localChannel); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		channels = append(channels, localChannel)
	}
	if len(channels) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "channel cannot be empty",
		})
		return
	}
	err = model.BatchInsertChannels(channels)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// BatchInsertChannels writes the database transaction directly. Refresh the
	// routing cache before reporting success so a newly created enabled channel
	// is immediately selectable when memory caching is enabled.
	model.InitChannelCache()
	recordManageAudit(c, "channel.create", map[string]interface{}{
		"name":  addChannelRequest.Channel.Name,
		"type":  addChannelRequest.Channel.Type,
		"count": len(channels),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channelName := ""
	channelProxy := ""
	channelLookupFailed := false
	if existing, err := model.GetChannelById(id, false); err == nil && existing != nil {
		channelName = existing.Name
		channelProxy = existing.GetSetting().Proxy
	} else {
		channelLookupFailed = true
	}
	channel := model.Channel{Id: id}
	err := channel.Delete()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	if channelLookupFailed {
		service.ResetProxyClientCache()
	} else {
		service.InvalidateProxyClient(channelProxy)
	}
	recordManageAudit(c, "channel.delete", map[string]interface{}{
		"id":   id,
		"name": channelName,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	if rows > 0 {
		service.ResetProxyClientCache()
	}
	recordManageAudit(c, "channel.delete_disabled", map[string]interface{}{
		"count": rows,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

type ChannelTag struct {
	Tag            string  `json:"tag"`
	NewTag         *string `json:"new_tag"`
	Priority       *int64  `json:"priority"`
	Weight         *uint   `json:"weight"`
	ModelMapping   *string `json:"model_mapping"`
	Models         *string `json:"models"`
	Groups         *string `json:"groups"`
	ParamOverride  *string `json:"param_override"`
	HeaderOverride *string `json:"header_override"`
}

func DisableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.DisableChannelByTag(channelTag.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_disable", map[string]interface{}{
		"tag": channelTag.Tag,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EnableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.EnableChannelByTag(channelTag.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_enable", map[string]interface{}{
		"tag": channelTag.Tag,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EditTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	if channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "tag不能为空",
		})
		return
	}
	routingFields := model.Channel{}
	routingRequest := make(map[string]any)
	if channelTag.Models != nil {
		routingFields.Models = *channelTag.Models
		routingRequest["models"] = true
	}
	if channelTag.Groups != nil {
		routingFields.Group = *channelTag.Groups
		routingRequest["group"] = true
	}
	if len(routingRequest) > 0 {
		if err := normalizeChannelRoutingFields(&routingFields, false, routingRequest); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		if err := validateChannelRoutingLengths(&routingFields); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		if channelTag.Models != nil {
			channelTag.Models = common.GetPointer(routingFields.Models)
		}
		if channelTag.Groups != nil {
			channelTag.Groups = common.GetPointer(routingFields.Group)
		}
	}
	if (channelTag.ParamOverride != nil || channelTag.HeaderOverride != nil) &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.ChannelSensitiveWrite) {
		common.ApiErrorI18n(c, i18n.MsgAuthInsufficientPrivilege)
		return
	}
	if channelTag.ParamOverride != nil {
		trimmed := strings.TrimSpace(*channelTag.ParamOverride)
		if trimmed != "" && !json.Valid([]byte(trimmed)) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "参数覆盖必须是合法的 JSON 格式",
			})
			return
		}
		channelTag.ParamOverride = common.GetPointer[string](trimmed)
	}
	if channelTag.HeaderOverride != nil {
		trimmed := strings.TrimSpace(*channelTag.HeaderOverride)
		if trimmed != "" && !json.Valid([]byte(trimmed)) {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "请求头覆盖必须是合法的 JSON 格式",
			})
			return
		}
		channelTag.HeaderOverride = common.GetPointer[string](trimmed)
	}
	err = model.EditChannelByTag(channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight, channelTag.ParamOverride, channelTag.HeaderOverride)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_edit", map[string]interface{}{
		"tag": channelTag.Tag,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type ChannelBatch struct {
	Ids []int   `json:"ids"`
	Tag *string `json:"tag"`
}

func DeleteChannelBatch(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	deletedCount, err := model.BatchDeleteChannels(channelBatch.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	if deletedCount > 0 {
		service.ResetProxyClientCache()
	}
	recordManageAudit(c, "channel.delete_batch", map[string]interface{}{
		"count": deletedCount,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    deletedCount,
	})
	return
}

type PatchChannel struct {
	model.Channel
	MultiKeyMode *string `json:"multi_key_mode"`
	KeyMode      *string `json:"key_mode"` // 多key模式下密钥覆盖或者追加
}

type ChannelStatusRequest struct {
	Status int `json:"status"`
}

type ChannelStatusBatchRequest struct {
	Ids    []int `json:"ids"`
	Status int   `json:"status"`
}

func parseMultiKeyEditInput(channel *model.Channel, input string) ([]string, error) {
	trimmedInput := strings.TrimSpace(input)
	if trimmedInput == "" {
		return []string{}, nil
	}
	if channel.Type != constant.ChannelTypeVertexAi || channel.GetOtherSettings().VertexKeyType == dto.VertexKeyTypeAPIKey {
		return splitChannelKeys(trimmedInput), nil
	}
	if strings.HasPrefix(trimmedInput, "[") {
		return getVertexArrayKeys(trimmedInput)
	}
	normalizedKey, err := normalizeVertexCredentialJSON([]byte(trimmedInput))
	if err != nil {
		return nil, err
	}
	return []string{normalizedKey}, nil
}

// applyChannelEdit merges an API patch into the latest locked database row.
// In particular, multi-key rotations must use the latest key pool and disabled
// indexes rather than the stale snapshot that was displayed in the browser.
func applyChannelEdit(latest *model.Channel, patch *PatchChannel, requestData map[string]any) ([]string, error) {
	if latest == nil || patch == nil {
		return nil, errors.New("channel edit cannot be empty")
	}

	updateColumns := make([]string, 0, len(requestData)+1)
	keyProvided := strings.TrimSpace(patch.Key) != ""
	for field := range requestData {
		switch field {
		case "type":
			latest.Type = patch.Type
		case "openai_organization":
			latest.OpenAIOrganization = patch.OpenAIOrganization
		case "test_model":
			latest.TestModel = patch.TestModel
		case "name":
			latest.Name = patch.Name
		case "weight":
			latest.Weight = patch.Weight
		case "base_url":
			latest.BaseURL = patch.BaseURL
		case "other":
			latest.Other = patch.Other
		case "models":
			latest.Models = patch.Models
		case "group":
			latest.Group = patch.Group
		case "model_mapping":
			latest.ModelMapping = patch.ModelMapping
		case "status_code_mapping":
			latest.StatusCodeMapping = patch.StatusCodeMapping
		case "priority":
			latest.Priority = patch.Priority
		case "auto_ban":
			latest.AutoBan = patch.AutoBan
		case "other_info":
			latest.OtherInfo = patch.OtherInfo
		case "tag":
			latest.Tag = patch.Tag
		case "setting":
			latest.Setting = patch.Setting
		case "param_override":
			latest.ParamOverride = patch.ParamOverride
		case "header_override":
			latest.HeaderOverride = patch.HeaderOverride
		case "remark":
			latest.Remark = patch.Remark
		case "settings":
			latest.OtherSettings = patch.OtherSettings
		case "key":
			if !keyProvided {
				continue
			}
		default:
			continue
		}
		updateColumns = append(updateColumns, field)
	}

	if patch.MultiKeyMode != nil && *patch.MultiKeyMode != "" {
		mode := constant.MultiKeyMode(*patch.MultiKeyMode)
		if !isValidMultiKeyMode(mode) {
			return nil, errors.New("不支持的多密钥轮询模式")
		}
		if latest.ChannelInfo.IsMultiKey {
			latest.ChannelInfo.MultiKeyMode = mode
		}
	}

	if latest.ChannelInfo.IsMultiKey {
		keyMode := patch.KeyMode
		if keyProvided && keyMode == nil {
			// Older clients did not send key_mode. A supplied key replaces the
			// pool so old disabled indexes cannot attach to unrelated secrets.
			replaceMode := "replace"
			keyMode = &replaceMode
		}
		if keyMode != nil {
			switch *keyMode {
			case "append":
				if keyProvided {
					newKeys, err := parseMultiKeyEditInput(latest, patch.Key)
					if err != nil {
						return nil, fmt.Errorf("追加密钥解析失败: %w", err)
					}
					existingKeys := latest.GetKeys()
					seen := make(map[string]struct{}, len(existingKeys)+len(newKeys))
					for _, key := range existingKeys {
						seen[strings.TrimSpace(key)] = struct{}{}
					}
					for _, key := range newKeys {
						key = strings.TrimSpace(key)
						if key == "" {
							continue
						}
						if _, exists := seen[key]; exists {
							continue
						}
						seen[key] = struct{}{}
						existingKeys = append(existingKeys, key)
					}
					latest.Key = strings.Join(existingKeys, "\n")
				}
			case "replace":
				if keyProvided {
					replacementKeys, err := parseMultiKeyEditInput(latest, patch.Key)
					if err != nil {
						return nil, fmt.Errorf("覆盖密钥解析失败: %w", err)
					}
					replacementKey := strings.Join(replacementKeys, "\n")
					if replacementKey != strings.Join(latest.GetKeys(), "\n") {
						latest.ChannelInfo.MultiKeyPollingIndex = 0
						latest.ChannelInfo.MultiKeyStatusList = nil
						latest.ChannelInfo.MultiKeyDisabledTime = nil
						latest.ChannelInfo.MultiKeyDisabledReason = nil
					}
					latest.Key = replacementKey
				}
			default:
				return nil, errors.New("不支持的密钥更新模式")
			}
		}
		if patch.MultiKeyMode != nil || patch.KeyMode != nil || keyProvided {
			updateColumns = append(updateColumns, "channel_info")
		}
	} else if keyProvided {
		key := strings.TrimSpace(patch.Key)
		if latest.Type == constant.ChannelTypeVertexAi && latest.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			normalizedKey, err := normalizeVertexCredentialJSON([]byte(key))
			if err != nil {
				return nil, fmt.Errorf("Vertex AI key 格式错误: %w", err)
			}
			key = normalizedKey
		}
		latest.Key = key
	}

	return updateColumns, nil
}

func UpdateChannel(c *gin.Context) {
	channel := PatchChannel{}
	rawBody, err := c.GetRawData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := common.Unmarshal(rawBody, &channel); err != nil {
		common.ApiError(c, err)
		return
	}
	var requestData map[string]any
	if err := common.Unmarshal(rawBody, &requestData); err != nil {
		common.ApiError(c, err)
		return
	}
	if _, ok := requestData["status"]; ok {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if _, ok := requestData["type"]; ok {
		if _, valid := constant.ChannelTypeNames[channel.Type]; !valid || channel.Type == constant.ChannelTypeUnknown {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
	}
	clearChannelReadOnlyFields(&channel, requestData)
	if err := normalizeChannelRoutingFields(&channel.Channel, false, requestData); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	canEditSensitive := authz.Can(c.GetInt("id"), c.GetInt("role"), authz.ChannelSensitiveWrite)
	permissionErr := errors.New("channel sensitive write permission required")
	var originChannel model.Channel
	var originProxy string
	proxyChanged := false

	// Use the same lock order as key selection and key-status management, then
	// merge the patch into a freshly loaded row under a database row lock.
	lock := model.GetChannelPollingLock(channel.Id)
	lock.Lock()
	updatedChannel, err := model.MutateChannelSelected(channel.Id, func(latest *model.Channel) ([]string, error) {
		originChannel = *latest
		originProxy = latest.GetSetting().Proxy
		if channelHasSensitiveChanges(&channel, latest, requestData) && !canEditSensitive {
			return nil, permissionErr
		}
		updateColumns, applyErr := applyChannelEdit(latest, &channel, requestData)
		if applyErr != nil {
			return nil, applyErr
		}
		if validationErr := validateChannel(latest, false); validationErr != nil {
			return nil, validationErr
		}
		if _, settingProvided := requestData["setting"]; settingProvided {
			newProxy, _ := service.NormalizeProxyURL(latest.GetSetting().Proxy)
			normalizedOriginProxy, originProxyErr := service.NormalizeProxyURL(originProxy)
			proxyChanged = originProxyErr != nil || normalizedOriginProxy != newProxy
		}
		return updateColumns, nil
	})
	lock.Unlock()
	if err != nil {
		if errors.Is(err, permissionErr) {
			common.ApiErrorI18n(c, i18n.MsgAuthInsufficientPrivilege)
			return
		}
		common.ApiError(c, err)
		return
	}
	channel.Channel = *updatedChannel
	model.InitChannelCache()
	if proxyChanged {
		service.InvalidateProxyClient(originProxy)
	}
	// 记录变更的字段名（语言无关的字段标识），密钥仅记录"已更换"绝不记录内容。
	changedFields := make([]string, 0)
	if channel.Models != originChannel.Models {
		changedFields = append(changedFields, "models")
	}
	if channel.Group != originChannel.Group {
		changedFields = append(changedFields, "group")
	}
	if channel.Type != originChannel.Type {
		changedFields = append(changedFields, "type")
	}
	if !equalStringPtr(channel.BaseURL, originChannel.BaseURL) {
		changedFields = append(changedFields, "base_url")
	}
	if channel.Key != "" && channel.Key != originChannel.Key {
		changedFields = append(changedFields, "key")
	}
	recordManageAudit(c, "channel.update", map[string]interface{}{
		"id":             channel.Id,
		"name":           channel.Name,
		"changed_fields": changedFields,
	})
	channel.Key = ""
	clearChannelInfo(&channel.Channel)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

func UpdateChannelStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	req := ChannelStatusRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || !isManageableChannelStatus(req.Status) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	changed := model.UpdateChannelStatus(id, "", req.Status, "manual operation")
	recordManageAudit(c, "channel.status_update", map[string]interface{}{
		"id":      id,
		"status":  req.Status,
		"changed": changed,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    changed,
	})
}

func BatchUpdateChannelStatus(c *gin.Context) {
	req := ChannelStatusBatchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Ids) == 0 || !isManageableChannelStatus(req.Status) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	changedCount := model.BatchUpdateChannelStatus(req.Ids, req.Status, "manual batch operation")
	recordManageAudit(c, "channel.status_update_batch", map[string]interface{}{
		"count":  changedCount,
		"total":  len(req.Ids),
		"status": req.Status,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    changedCount,
	})
}

func isManageableChannelStatus(status int) bool {
	return status == common.ChannelStatusEnabled || status == common.ChannelStatusManuallyDisabled
}

// equalStringPtr 比较两个 *string 是否相等（均为 nil 视为相等）。
func equalStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

type fetchModelsRequest struct {
	ChannelID      int     `json:"channel_id"`
	BaseURL        *string `json:"base_url"`
	Type           int     `json:"type"`
	Key            string  `json:"key"`
	AdvancedCustom *string `json:"advanced_custom"`
	HeaderOverride *string `json:"header_override"`
	Proxy          *string `json:"proxy"`
}

func buildAdvancedCustomModelPreviewChannel(req fetchModelsRequest) (*model.Channel, error) {
	var channel *model.Channel
	if req.ChannelID > 0 {
		savedChannel, err := model.GetChannelById(req.ChannelID, true)
		if err != nil {
			return nil, err
		}
		if savedChannel.Type != constant.ChannelTypeAdvancedCustom {
			return nil, fmt.Errorf("channel %d is not an advanced custom channel", req.ChannelID)
		}
		channel = savedChannel
	} else {
		key := strings.TrimSpace(req.Key)
		if key != "" {
			key = strings.Split(key, "\n")[0]
		}
		channel = &model.Channel{
			Type: req.Type,
			Key:  key,
		}
	}

	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return nil, fmt.Errorf("channel type must be advanced custom")
	}
	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)
		channel.BaseURL = &baseURL
	}

	settings := channel.GetOtherSettings()
	if req.AdvancedCustom != nil {
		rawConfig := strings.TrimSpace(*req.AdvancedCustom)
		if rawConfig == "" {
			return nil, fmt.Errorf("advanced_custom is required")
		}
		var config dto.AdvancedCustomConfig
		if err := common.UnmarshalJsonStr(rawConfig, &config); err != nil {
			return nil, err
		}
		settings.AdvancedCustom = &config
	} else if req.ChannelID <= 0 {
		return nil, fmt.Errorf("advanced_custom is required")
	}
	channel.SetOtherSettings(settings)

	if req.HeaderOverride != nil {
		rawHeaderOverride := strings.TrimSpace(*req.HeaderOverride)
		if rawHeaderOverride != "" {
			var headerOverride map[string]any
			if err := common.UnmarshalJsonStr(rawHeaderOverride, &headerOverride); err != nil {
				return nil, fmt.Errorf("header_override must be a JSON object: %w", err)
			}
		}
		channel.HeaderOverride = &rawHeaderOverride
	}
	if req.Proxy != nil {
		channelSettings := channel.GetSetting()
		channelSettings.Proxy = strings.TrimSpace(*req.Proxy)
		channel.SetSetting(channelSettings)
	}

	if err := validateChannel(channel, false); err != nil {
		return nil, err
	}
	return channel, nil
}

func FetchModels(c *gin.Context) {
	var req fetchModelsRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	var channel *model.Channel
	if req.Type == constant.ChannelTypeAdvancedCustom || req.ChannelID > 0 {
		var err error
		channel, err = buildAdvancedCustomModelPreviewChannel(req)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	} else {
		baseURL := ""
		if req.BaseURL != nil {
			baseURL = strings.TrimSpace(*req.BaseURL)
		}
		if baseURL == "" {
			baseURL = constant.ChannelBaseURLs[req.Type]
		}

		key := strings.TrimSpace(req.Key)
		if req.Type != constant.ChannelTypeCodex {
			key = strings.Split(key, "\n")[0]
		}
		channel = &model.Channel{
			Type:    req.Type,
			Key:     key,
			BaseURL: &baseURL,
		}
	}

	models, err := fetchChannelUpstreamModelIDs(channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("获取模型列表失败: %s", err.Error()),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
}

func BatchSetChannelTag(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.BatchSetChannelTag(channelBatch.Ids, channelBatch.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_batch_set", map[string]interface{}{
		"count": len(channelBatch.Ids),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    len(channelBatch.Ids),
	})
	return
}

func GetTagModels(c *gin.Context) {
	tag := c.Query("tag")
	if tag == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "tag不能为空",
		})
		return
	}

	channels, err := model.GetChannelsByTag(tag, false, false) // idSort=false, selectAll=false
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var longestModels string
	maxLength := 0

	// Find the longest models string among all channels with the given tag
	for _, channel := range channels {
		if channel.Models != "" {
			currentModels := strings.Split(channel.Models, ",")
			if len(currentModels) > maxLength {
				maxLength = len(currentModels)
				longestModels = channel.Models
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    longestModels,
	})
	return
}

// CopyChannel handles cloning an existing channel with its key.
// POST /api/channel/copy/:id
// Optional query params:
//
//	suffix         - string appended to the original name (default "_复制")
//	reset_balance  - bool, when true will reset balance & used_quota to 0 (default true)
func CopyChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	suffix := c.DefaultQuery("suffix", "_复制")
	resetBalance := true
	if rbStr := c.DefaultQuery("reset_balance", "true"); rbStr != "" {
		if v, err := strconv.ParseBool(rbStr); err == nil {
			resetBalance = v
		}
	}

	// fetch original channel with key
	origin, err := model.GetChannelById(id, true)
	if err != nil {
		common.SysError("failed to get channel by id: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "获取渠道信息失败，请稍后重试"})
		return
	}

	// clone channel
	clone := *origin // shallow copy is sufficient as we will overwrite primitives
	clone.Id = 0     // let DB auto-generate
	clone.CreatedTime = common.GetTimestamp()
	clone.Name = origin.Name + suffix
	clone.TestTime = 0
	clone.ResponseTime = 0
	clone.Keys = nil
	clone.ChannelInfo.MultiKeyPollingIndex = 0
	clone.ChannelInfo.MultiKeyStatusList = nil
	clone.ChannelInfo.MultiKeyDisabledTime = nil
	clone.ChannelInfo.MultiKeyDisabledReason = nil
	if clone.ChannelInfo.IsMultiKey {
		clone.ChannelInfo.MultiKeySize = len(clone.GetKeys())
	} else {
		clone.ChannelInfo = model.ChannelInfo{}
	}
	if resetBalance {
		clone.Balance = 0
		clone.BalanceUpdatedTime = 0
		clone.UsedQuota = 0
	}

	if err := clone.ValidateSettings(); err != nil {
		common.SysError("failed to validate cloned channel: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Failed to copy channel: invalid channel settings"})
		return
	}
	if err := validateChannel(&clone, true); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// insert
	if err := clone.Insert(); err != nil {
		common.SysError("failed to clone channel: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "复制渠道失败，请稍后重试"})
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.copy", map[string]interface{}{
		"sourceId": id,
		"id":       clone.Id,
		"name":     clone.Name,
	})
	// success
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"id": clone.Id}})
}

// MultiKeyManageRequest represents the request for multi-key management operations
type MultiKeyManageRequest struct {
	ChannelId int    `json:"channel_id"`
	Action    string `json:"action"`              // "disable_key", "enable_key", "delete_key", "delete_disabled_keys", "get_key_status"
	KeyIndex  *int   `json:"key_index,omitempty"` // for disable_key, enable_key, and delete_key actions
	Page      int    `json:"page,omitempty"`      // for get_key_status pagination
	PageSize  int    `json:"page_size,omitempty"` // for get_key_status pagination
	Status    *int   `json:"status,omitempty"`    // for get_key_status filtering: 1=enabled, 2=manual_disabled, 3=auto_disabled, nil=all
}

// MultiKeyStatusResponse represents the response for key status query
type MultiKeyStatusResponse struct {
	Keys       []KeyStatus `json:"keys"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
	// Statistics
	EnabledCount        int `json:"enabled_count"`
	ManualDisabledCount int `json:"manual_disabled_count"`
	AutoDisabledCount   int `json:"auto_disabled_count"`
}

type KeyStatus struct {
	Index        int    `json:"index"`
	Status       int    `json:"status"` // 1: enabled, 2: disabled
	DisabledTime int64  `json:"disabled_time,omitempty"`
	Reason       string `json:"reason,omitempty"`
	KeyPreview   string `json:"key_preview"` // first 10 chars of key for identification
}

const multiKeyStatusMaxPageSize = 200

type multiKeyManageMutationResult struct {
	message string
	data    any
	columns []string
}

func syncMultiKeyChannelAvailability(channel *model.Channel, result *multiKeyManageMutationResult, now int64) {
	hasEnabledKey := false
	for index := range channel.GetKeys() {
		status := common.ChannelStatusEnabled
		if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists {
			status = storedStatus
		}
		if status == common.ChannelStatusEnabled {
			hasEnabledKey = true
			break
		}
	}
	if hasEnabledKey && channel.Status == common.ChannelStatusAutoDisabled {
		channel.Status = common.ChannelStatusEnabled
		info := channel.GetOtherInfo()
		delete(info, "status_reason")
		delete(info, "status_time")
		channel.SetOtherInfo(info)
	} else if !hasEnabledKey && channel.Status == common.ChannelStatusEnabled {
		channel.Status = common.ChannelStatusAutoDisabled
		info := channel.GetOtherInfo()
		info["status_reason"] = "All keys are disabled"
		info["status_time"] = now
		channel.SetOtherInfo(info)
	} else {
		return
	}
	result.columns = append(result.columns, "status", "other_info")
}

func applyMultiKeyManageMutation(channel *model.Channel, request MultiKeyManageRequest, now int64) (multiKeyManageMutationResult, error) {
	result := multiKeyManageMutationResult{columns: []string{"channel_info"}}
	if channel == nil || !channel.ChannelInfo.IsMultiKey {
		return result, errors.New("该渠道不是多密钥模式")
	}
	keys := channel.GetKeys()
	channel.ChannelInfo.MultiKeySize = len(keys)

	switch request.Action {
	case "disable_key":
		if request.KeyIndex == nil {
			return result, errors.New("未指定要禁用的密钥索引")
		}
		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= len(keys) {
			return result, errors.New("密钥索引超出范围")
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}
		channel.ChannelInfo.MultiKeyStatusList[keyIndex] = common.ChannelStatusManuallyDisabled
		channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = now
		channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = "manual operation"
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = "密钥已禁用"
		return result, nil

	case "enable_key":
		if request.KeyIndex == nil {
			return result, errors.New("未指定要启用的密钥索引")
		}
		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= len(keys) {
			return result, errors.New("密钥索引超出范围")
		}
		delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
		delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = "密钥已启用"
		return result, nil

	case "enable_all_keys":
		enabledCount := 0
		for index := range keys {
			if status, disabled := channel.ChannelInfo.MultiKeyStatusList[index]; disabled && status != common.ChannelStatusEnabled {
				enabledCount++
			}
		}
		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = fmt.Sprintf("已启用 %d 个密钥", enabledCount)
		return result, nil

	case "disable_all_keys":
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}
		disabledCount := 0
		for index := range keys {
			status := common.ChannelStatusEnabled
			if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists {
				status = storedStatus
			}
			if status != common.ChannelStatusEnabled {
				continue
			}
			channel.ChannelInfo.MultiKeyStatusList[index] = common.ChannelStatusManuallyDisabled
			channel.ChannelInfo.MultiKeyDisabledTime[index] = now
			channel.ChannelInfo.MultiKeyDisabledReason[index] = "manual operation"
			disabledCount++
		}
		if disabledCount == 0 {
			return result, errors.New("没有可禁用的密钥")
		}
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = fmt.Sprintf("已禁用 %d 个密钥", disabledCount)
		return result, nil

	case "delete_key":
		if request.KeyIndex == nil {
			return result, errors.New("未指定要删除的密钥索引")
		}
		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= len(keys) {
			return result, errors.New("密钥索引超出范围")
		}
		if len(keys) == 1 {
			return result, errors.New("不能删除最后一个密钥")
		}
		remainingKeys := make([]string, 0, len(keys)-1)
		newStatusList := make(map[int]int)
		newDisabledTime := make(map[int]int64)
		newDisabledReason := make(map[int]string)
		for oldIndex, key := range keys {
			if oldIndex == keyIndex {
				continue
			}
			newIndex := len(remainingKeys)
			remainingKeys = append(remainingKeys, key)
			if status, exists := channel.ChannelInfo.MultiKeyStatusList[oldIndex]; exists && status != common.ChannelStatusEnabled {
				newStatusList[newIndex] = status
			}
			if disabledAt, exists := channel.ChannelInfo.MultiKeyDisabledTime[oldIndex]; exists {
				newDisabledTime[newIndex] = disabledAt
			}
			if reason, exists := channel.ChannelInfo.MultiKeyDisabledReason[oldIndex]; exists {
				newDisabledReason[newIndex] = reason
			}
		}
		pollingIndex := channel.ChannelInfo.MultiKeyPollingIndex
		if keyIndex < pollingIndex {
			pollingIndex--
		}
		if pollingIndex < 0 || pollingIndex >= len(remainingKeys) {
			pollingIndex = 0
		}
		channel.Key = strings.Join(remainingKeys, "\n")
		channel.ChannelInfo.MultiKeySize = len(remainingKeys)
		channel.ChannelInfo.MultiKeyPollingIndex = pollingIndex
		channel.ChannelInfo.MultiKeyStatusList = newStatusList
		channel.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
		channel.ChannelInfo.MultiKeyDisabledReason = newDisabledReason
		result.columns = []string{"key", "channel_info"}
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = "密钥已删除"
		return result, nil

	case "delete_disabled_keys":
		remainingKeys := make([]string, 0, len(keys))
		newStatusList := make(map[int]int)
		newDisabledTime := make(map[int]int64)
		newDisabledReason := make(map[int]string)
		newIndexByOld := make(map[int]int, len(keys))
		deletedCount := 0
		for oldIndex, key := range keys {
			status := common.ChannelStatusEnabled
			if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[oldIndex]; exists {
				status = storedStatus
			}
			if status == common.ChannelStatusAutoDisabled {
				deletedCount++
				continue
			}
			newIndex := len(remainingKeys)
			newIndexByOld[oldIndex] = newIndex
			remainingKeys = append(remainingKeys, key)
			if status != common.ChannelStatusEnabled {
				newStatusList[newIndex] = status
			}
			if disabledAt, exists := channel.ChannelInfo.MultiKeyDisabledTime[oldIndex]; exists {
				newDisabledTime[newIndex] = disabledAt
			}
			if reason, exists := channel.ChannelInfo.MultiKeyDisabledReason[oldIndex]; exists {
				newDisabledReason[newIndex] = reason
			}
		}
		if deletedCount == 0 {
			return result, errors.New("没有需要删除的自动禁用密钥")
		}
		if len(remainingKeys) == 0 {
			return result, errors.New("不能删除所有密钥")
		}
		pollingIndex := 0
		oldPollingIndex := channel.ChannelInfo.MultiKeyPollingIndex
		if oldPollingIndex < 0 || oldPollingIndex >= len(keys) {
			oldPollingIndex = 0
		}
		for offset := 0; offset < len(keys); offset++ {
			oldIndex := (oldPollingIndex + offset) % len(keys)
			if newIndex, exists := newIndexByOld[oldIndex]; exists {
				pollingIndex = newIndex
				break
			}
		}
		channel.Key = strings.Join(remainingKeys, "\n")
		channel.ChannelInfo.MultiKeySize = len(remainingKeys)
		channel.ChannelInfo.MultiKeyPollingIndex = pollingIndex
		channel.ChannelInfo.MultiKeyStatusList = newStatusList
		channel.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
		channel.ChannelInfo.MultiKeyDisabledReason = newDisabledReason
		result.columns = []string{"key", "channel_info"}
		syncMultiKeyChannelAvailability(channel, &result, now)
		result.message = fmt.Sprintf("已删除 %d 个自动禁用的密钥", deletedCount)
		result.data = deletedCount
		return result, nil
	}

	return result, errors.New("不支持的操作")
}

// ManageMultiKeys handles multi-key management operations
func ManageMultiKeys(c *gin.Context) {
	request := MultiKeyManageRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.ChannelId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if multiKeyActionRequiresSensitiveWrite(request.Action) &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.ChannelSensitiveWrite) {
		common.ApiErrorI18n(c, i18n.MsgAuthInsufficientPrivilege)
		return
	}

	// Keep local key selection and management ordered. Database row locking in
	// MutateChannelSelected provides the corresponding cross-instance guard.
	lock := model.GetChannelPollingLock(request.ChannelId)
	lock.Lock()
	defer lock.Unlock()

	channel, err := model.GetChannelById(request.ChannelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "渠道不存在",
		})
		return
	}
	if !channel.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该渠道不是多密钥模式",
		})
		return
	}

	if request.Action != "get_key_status" {
		var mutationResult multiKeyManageMutationResult
		_, err = model.MutateChannelSelected(request.ChannelId, func(latest *model.Channel) ([]string, error) {
			var mutationErr error
			mutationResult, mutationErr = applyMultiKeyManageMutation(latest, request, common.GetTimestamp())
			return mutationResult.columns, mutationErr
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		model.InitChannelCache()
		recordManageAudit(c, "channel.multi_key_manage", map[string]interface{}{
			"action": request.Action,
			"id":     channel.Id,
		})
		response := gin.H{
			"success": true,
			"message": mutationResult.message,
		}
		if mutationResult.data != nil {
			response["data"] = mutationResult.data
		}
		c.JSON(http.StatusOK, response)
		return
	}

	markAuditLogged(c)
	keys := channel.GetKeys()
	page := request.Page
	pageSize := request.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	} else if pageSize > multiKeyStatusMaxPageSize {
		pageSize = multiKeyStatusMaxPageSize
	}

	enabledCount := 0
	manualDisabledCount := 0
	autoDisabledCount := 0
	allKeyStatusList := make([]KeyStatus, 0, len(keys))
	for index, key := range keys {
		status := common.ChannelStatusEnabled
		if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists {
			status = storedStatus
		}
		switch status {
		case common.ChannelStatusEnabled:
			enabledCount++
		case common.ChannelStatusManuallyDisabled:
			manualDisabledCount++
		case common.ChannelStatusAutoDisabled:
			autoDisabledCount++
		}

		disabledTime := int64(0)
		reason := ""
		if status != common.ChannelStatusEnabled {
			disabledTime = channel.ChannelInfo.MultiKeyDisabledTime[index]
			reason = channel.ChannelInfo.MultiKeyDisabledReason[index]
		}
		keyPreview := key
		keyRunes := []rune(keyPreview)
		if len(keyRunes) > 10 {
			keyPreview = string(keyRunes[:10]) + "..."
		}
		allKeyStatusList = append(allKeyStatusList, KeyStatus{
			Index:        index,
			Status:       status,
			DisabledTime: disabledTime,
			Reason:       reason,
			KeyPreview:   keyPreview,
		})
	}

	filteredKeyStatusList := allKeyStatusList
	if request.Status != nil {
		filteredKeyStatusList = make([]KeyStatus, 0, len(allKeyStatusList))
		for _, keyStatus := range allKeyStatusList {
			if keyStatus.Status == *request.Status {
				filteredKeyStatusList = append(filteredKeyStatusList, keyStatus)
			}
		}
	}
	filteredTotal := len(filteredKeyStatusList)
	totalPages := (filteredTotal + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > filteredTotal {
		end = filteredTotal
	}
	pageKeyStatusList := make([]KeyStatus, 0)
	if start < filteredTotal {
		pageKeyStatusList = filteredKeyStatusList[start:end]
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": MultiKeyStatusResponse{
			Keys:                pageKeyStatusList,
			Total:               filteredTotal,
			Page:                page,
			PageSize:            pageSize,
			TotalPages:          totalPages,
			EnabledCount:        enabledCount,
			ManualDisabledCount: manualDisabledCount,
			AutoDisabledCount:   autoDisabledCount,
		},
	})
}
func multiKeyActionRequiresSensitiveWrite(action string) bool {
	return action == "delete_key" || action == "delete_disabled_keys"
}

// OllamaPullModel 拉取 Ollama 模型
func OllamaPullModel(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	key := strings.Split(channel.Key, "\n")[0]
	err = ollama.PullOllamaModel(baseURL, key, req.ModelName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to pull model: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s pulled successfully", req.ModelName),
	})
}

// OllamaPullModelStream 流式拉取 Ollama 模型
func OllamaPullModelStream(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	// 设置 SSE 头部
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	key := strings.Split(channel.Key, "\n")[0]

	// 创建进度回调函数
	progressCallback := func(progress ollama.OllamaPullResponse) {
		data, _ := common.Marshal(progress)
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(data))
		c.Writer.Flush()
	}

	// 执行拉取
	err = ollama.PullOllamaModelStream(baseURL, key, req.ModelName, progressCallback)

	if err != nil {
		errorData, _ := common.Marshal(gin.H{
			"error": err.Error(),
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(errorData))
	} else {
		successData, _ := common.Marshal(gin.H{
			"message": fmt.Sprintf("Model %s pulled successfully", req.ModelName),
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(successData))
	}

	// 发送结束标志
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

// OllamaDeleteModel 删除 Ollama 模型
func OllamaDeleteModel(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	key := strings.Split(channel.Key, "\n")[0]
	err = ollama.DeleteOllamaModel(baseURL, key, req.ModelName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to delete model: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s deleted successfully", req.ModelName),
	})
}

// OllamaVersion 获取 Ollama 服务版本信息
func OllamaVersion(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	key := strings.Split(channel.Key, "\n")[0]
	version, err := ollama.FetchOllamaVersion(baseURL, key)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("获取Ollama版本失败: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"version": version,
		},
	})
}
