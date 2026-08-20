package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInviteTopupRewardRatioRejectsInvalidValuesWithoutMutatingState(t *testing.T) {
	oldRatio := common.InviteTopupRewardRatio
	common.InviteTopupRewardRatio = 0.25
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldValue := common.OptionMap["InviteTopupRewardRatio"]
	common.OptionMap["InviteTopupRewardRatio"] = "0.25"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.InviteTopupRewardRatio = oldRatio
		common.OptionMapRWMutex.Lock()
		if oldValue == "" {
			delete(common.OptionMap, "InviteTopupRewardRatio")
		} else {
			common.OptionMap["InviteTopupRewardRatio"] = oldValue
		}
		common.OptionMapRWMutex.Unlock()
	})

	for _, value := range []string{"-0.1", "1.1", "NaN", "+Inf", "invalid"} {
		t.Run(value, func(t *testing.T) {
			require.Error(t, updateOptionMap("InviteTopupRewardRatio", value))
			assert.Equal(t, 0.25, common.InviteTopupRewardRatio)
			common.OptionMapRWMutex.RLock()
			assert.Equal(t, "0.25", common.OptionMap["InviteTopupRewardRatio"])
			common.OptionMapRWMutex.RUnlock()
		})
	}
}

func TestInviteTopupRewardRatioAcceptsBoundaries(t *testing.T) {
	oldRatio := common.InviteTopupRewardRatio
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldValue := common.OptionMap["InviteTopupRewardRatio"]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.InviteTopupRewardRatio = oldRatio
		if oldValue == "" {
			delete(common.OptionMap, "InviteTopupRewardRatio")
		} else {
			common.OptionMap["InviteTopupRewardRatio"] = oldValue
		}
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, updateOptionMap("InviteTopupRewardRatio", "0"))
	assert.Zero(t, common.InviteTopupRewardRatio)
	require.NoError(t, updateOptionMap("InviteTopupRewardRatio", "1"))
	assert.Equal(t, 1.0, common.InviteTopupRewardRatio)
	require.NoError(t, updateOptionMap("InviteTopupRewardRatio", " 0.10 "))
	assert.Equal(t, 0.1, common.InviteTopupRewardRatio)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, "0.1", common.OptionMap["InviteTopupRewardRatio"])
	common.OptionMapRWMutex.RUnlock()
}

func TestInviteCountPersistsWhenInviterRewardQuotaIsZero(t *testing.T) {
	truncateTables(t)
	oldQuota := common.QuotaForInviter
	common.QuotaForInviter = 0
	t.Cleanup(func() { common.QuotaForInviter = oldQuota })

	inviter := insertUserForPaymentGuardTest(t, 520, 0)
	invitee := &User{
		Id:       521,
		Username: "invite_count_invitee",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, invitee.Insert(inviter.Id))

	count, err := CountUsersByInviterId(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	var reloadedInviter User
	require.NoError(t, DB.Select("aff_count", "aff_quota", "aff_history").First(&reloadedInviter, inviter.Id).Error)
	assert.Equal(t, 1, reloadedInviter.AffCount)
	assert.Zero(t, reloadedInviter.AffQuota)
	assert.Zero(t, reloadedInviter.AffHistoryQuota)
}
