package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

func GetChannel(group string, model string, retry int, requestPath string) (*Channel, error) {
	var abilities []Ability
	err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).
		Order("weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	abilities = filterAbilitiesByRequestPathAndModel(abilities, requestPath, model)
	if len(abilities) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		if normalizedModel != "" && normalizedModel != model {
			err = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, normalizedModel, true).
				Order("weight DESC").Find(&abilities).Error
			if err != nil {
				return nil, err
			}
			abilities = filterAbilitiesByRequestPathAndModel(abilities, requestPath, model)
		}
	}
	if len(abilities) == 0 {
		return nil, nil
	}

	uniquePriorities := make(map[int64]struct{})
	for _, ability := range abilities {
		priority := int64(0)
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		uniquePriorities[priority] = struct{}{}
	}
	priorities := make([]int64, 0, len(uniquePriorities))
	for priority := range uniquePriorities {
		priorities = append(priorities, priority)
	}
	sort.Slice(priorities, func(left, right int) bool {
		return priorities[left] > priorities[right]
	})
	if retry < 0 {
		retry = 0
	}
	if retry >= len(priorities) {
		retry = len(priorities) - 1
	}
	targetPriority := priorities[retry]
	targetAbilities := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		priority := int64(0)
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		if priority == targetPriority {
			targetAbilities = append(targetAbilities, ability)
		}
	}

	channel := Channel{}
	weights := make([]uint64, len(targetAbilities))
	for index, ability := range targetAbilities {
		weights[index] = selectionWeightFromUint(ability.Weight)
	}
	selectedIndex := weightedSelectionIndex(weights, common.GetRandomInt)
	if selectedIndex < 0 {
		return nil, errors.New("channel selection failed")
	}
	channel.Id = targetAbilities[selectedIndex].ChannelId
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

func selectionWeightFromUint(weight uint) uint64 {
	const adjustment = uint64(10)
	converted := uint64(weight)
	if converted > ^uint64(0)-adjustment {
		return ^uint64(0)
	}
	return converted + adjustment
}

// weightedSelectionIndex scales all weights by the same divisor before using
// the project's int-based random helper. This preserves useful routing ratios
// while preventing a saturated first candidate from taking 100% of traffic
// when two or more configured weights exceed the native int range.
func weightedSelectionIndex(weights []uint64, randomInt func(int) int) int {
	if len(weights) == 0 || randomInt == nil {
		return -1
	}

	maxWeight := uint64(0)
	for _, weight := range weights {
		if weight > maxWeight {
			maxWeight = weight
		}
	}
	if maxWeight == 0 {
		return randomInt(len(weights))
	}

	maxInt := int(^uint(0) >> 1)
	maxPerCandidate := uint64(maxInt / len(weights))
	if maxPerCandidate == 0 {
		return -1
	}
	divisor := uint64(1)
	if maxWeight > maxPerCandidate {
		divisor = maxWeight / maxPerCandidate
		if maxWeight%maxPerCandidate != 0 {
			divisor++
		}
	}

	scaled := make([]int, len(weights))
	total := 0
	for index, weight := range weights {
		if weight == 0 {
			continue
		}
		scaledWeight := weight / divisor
		if scaledWeight == 0 {
			scaledWeight = 1
		}
		scaled[index] = int(scaledWeight)
		total += scaled[index]
	}
	if total <= 0 {
		return -1
	}

	randomWeight := randomInt(total)
	for index, weight := range scaled {
		if randomWeight < weight {
			return index
		}
		randomWeight -= weight
	}
	return -1
}

// filterAbilitiesByRequestPathAndModel restricts candidates by request path and
// model for the DB (non-memory-cache) selection path. Only Advanced Custom
// (type 58) channels are path-checked: kept only when one of their routes matches
// requestPath and model; all other channel types always pass. When requestPath is
// empty, filtering is skipped.
func filterAbilitiesByRequestPathAndModel(abilities []Ability, requestPath string, model string) []Ability {
	if requestPath == "" || len(abilities) == 0 {
		return abilities
	}

	channelIds := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIds = append(channelIds, ability.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		// On error, fall back to unfiltered candidates to avoid blocking selection
		return abilities
	}

	advancedConfigs := make(map[int]*dto.AdvancedCustomConfig)
	for _, channel := range channels {
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			advancedConfigs[channel.Id] = channel.GetOtherSettings().AdvancedCustom
		}
	}

	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		config, isAdvancedCustom := advancedConfigs[ability.ChannelId]
		if !isAdvancedCustom {
			filtered = append(filtered, ability)
			continue
		}
		if config != nil && config.SupportsPathForModel(requestPath, model) {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[[2]string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := [2]string{group, model}
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	if tx == nil {
		return DB.Transaction(func(tx *gorm.DB) error {
			return channel.UpdateAbilities(tx)
		})
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		return err
	}

	// Then add new abilities
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[[2]string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := [2]string{group, model}
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, errors.New("已经有一个修复任务在运行中，请稍后再试")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(fmt.Sprintf("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}
