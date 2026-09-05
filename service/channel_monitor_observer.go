package service

import (
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

const (
	channelMonitorObserverPendingSeconds  = int64(10)
	channelMonitorObserverNoTargetSeconds = int64(model.ChannelMonitorPassiveSampleSeconds)
	channelMonitorObserverCacheCapacity   = 4096
	channelMonitorObserverCacheTTL        = 10 * time.Minute
)

type channelMonitorObserverDeadline struct {
	nextAllowedAt atomic.Int64
}

func (deadline *channelMonitorObserverDeadline) completeReservation(reservedUntil, nextAllowedAt int64) {
	deadline.nextAllowedAt.CompareAndSwap(reservedUntil, nextAllowedAt)
}

var (
	channelMonitorObserverDeadlines = hot.NewHotCache[string, *channelMonitorObserverDeadline](
		hot.LRU,
		channelMonitorObserverCacheCapacity,
	).Build()
	channelMonitorObserverDeadlinesMu sync.Mutex
)

func channelMonitorObserverKey(group, modelName, endpointType string) string {
	return strings.TrimSpace(group) + "\x00" + strings.TrimSpace(modelName) + "\x00" + strings.TrimSpace(endpointType)
}

func channelMonitorObserverDeadlineFor(key string) *channelMonitorObserverDeadline {
	channelMonitorObserverDeadlinesMu.Lock()
	defer channelMonitorObserverDeadlinesMu.Unlock()
	if deadline, found, _ := channelMonitorObserverDeadlines.Get(key); found {
		channelMonitorObserverDeadlines.SetWithTTL(key, deadline, channelMonitorObserverCacheTTL)
		return deadline
	}
	deadline := &channelMonitorObserverDeadline{}
	channelMonitorObserverDeadlines.SetWithTTL(key, deadline, channelMonitorObserverCacheTTL)
	return deadline
}

// InvalidateChannelMonitorObserverCache makes monitor configuration changes
// visible to passive traffic immediately instead of waiting for a negative
// cache entry to expire.
func InvalidateChannelMonitorObserverCache(group, modelName string) {
	channelMonitorObserverDeadlinesMu.Lock()
	defer channelMonitorObserverDeadlinesMu.Unlock()
	for _, endpointType := range []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeJinaRerank,
		constant.EndpointTypeImageGeneration,
		constant.EndpointTypeEmbeddings,
	} {
		channelMonitorObserverDeadlines.Delete(channelMonitorObserverKey(group, modelName, string(endpointType)))
	}
}

func channelMonitorTrafficEndpointType(relayInfo *relaycommon.RelayInfo) (string, bool) {
	if relayInfo == nil {
		return "", false
	}
	requestURL, err := url.Parse(strings.TrimSpace(relayInfo.RequestURLPath))
	if err != nil {
		return "", false
	}
	requestPath := strings.TrimSuffix(requestURL.Path, "/")
	switch requestPath {
	case "/v1/chat/completions":
		return string(constant.EndpointTypeOpenAI), true
	case "/v1/responses":
		return string(constant.EndpointTypeOpenAIResponse), true
	case "/v1/responses/compact":
		return string(constant.EndpointTypeOpenAIResponseCompact), true
	case "/v1/messages":
		return string(constant.EndpointTypeAnthropic), true
	case "/v1/rerank":
		return string(constant.EndpointTypeJinaRerank), true
	case "/v1/images/generations":
		return string(constant.EndpointTypeImageGeneration), true
	case "/v1/embeddings":
		return string(constant.EndpointTypeEmbeddings), true
	}
	if strings.HasPrefix(requestPath, "/v1beta/models/") || strings.HasPrefix(requestPath, "/v1/models/") {
		if strings.Contains(requestPath, ":generateContent") || strings.Contains(requestPath, ":streamGenerateContent") {
			return string(constant.EndpointTypeGemini), true
		}
	}
	return "", false
}

func extractChannelMonitorTrafficSample(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, finished time.Time) (model.RecordChannelMonitorTrafficSuccessParams, bool) {
	if ctx == nil || relayInfo == nil || relayInfo.IsChannelTest || relayInfo.ChannelMeta == nil || relayInfo.ChannelId <= 0 {
		return model.RecordChannelMonitorTrafficSuccessParams{}, false
	}
	group := strings.TrimSpace(relayInfo.UsingGroup)
	modelName := strings.TrimSpace(relayInfo.OriginModelName)
	if group == "" || modelName == "" {
		return model.RecordChannelMonitorTrafficSuccessParams{}, false
	}
	if _, specified := common.GetContextKey(ctx, constant.ContextKeyTokenSpecificChannelId); specified {
		return model.RecordChannelMonitorTrafficSuccessParams{}, false
	}
	endpointType, supported := channelMonitorTrafficEndpointType(relayInfo)
	if !supported {
		return model.RecordChannelMonitorTrafficSuccessParams{}, false
	}
	if finished.IsZero() {
		finished = time.Now()
	}
	started := relayInfo.StartTime
	if started.IsZero() || started.After(finished) {
		started = finished
	}
	responseFinished := finished
	if relayInfo.IsStream && !relayInfo.FirstResponseTime.IsZero() {
		responseFinished = relayInfo.FirstResponseTime
	}
	responseTimeMs := responseFinished.Sub(started).Milliseconds()
	if responseTimeMs < 0 {
		responseTimeMs = 0
	}
	return model.RecordChannelMonitorTrafficSuccessParams{
		Group:          group,
		Model:          modelName,
		EndpointType:   endpointType,
		ChannelID:      relayInfo.ChannelId,
		StartedAt:      started.Unix(),
		FinishedAt:     finished.Unix(),
		ResponseTimeMs: responseTimeMs,
		Retried:        relayInfo.RetryIndex > 0,
	}, true
}

// ObserveSuccessfulRelayForChannelMonitor records the first eligible real
// request in each passive sample window. It copies all request data before
// starting background work because Gin contexts must not be read after the
// request completes.
func ObserveSuccessfulRelayForChannelMonitor(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) {
	sample, ok := extractChannelMonitorTrafficSample(ctx, relayInfo, time.Now())
	if !ok {
		return
	}
	key := channelMonitorObserverKey(sample.Group, sample.Model, sample.EndpointType)
	deadline := channelMonitorObserverDeadlineFor(key)
	reservedUntil := sample.FinishedAt + channelMonitorObserverPendingSeconds
	for {
		nextAllowedAt := deadline.nextAllowedAt.Load()
		if sample.FinishedAt < nextAllowedAt {
			return
		}
		if deadline.nextAllowedAt.CompareAndSwap(nextAllowedAt, reservedUntil) {
			break
		}
	}

	gopool.Go(func() {
		result, err := model.RecordChannelMonitorTrafficSuccess(sample)
		if err != nil {
			deadline.completeReservation(reservedUntil, reservedUntil)
			common.SysError("failed to record channel monitor traffic: " + err.Error())
			return
		}
		nextAllowedAt := result.NextAllowedAt
		if nextAllowedAt <= sample.FinishedAt {
			nextAllowedAt = sample.FinishedAt + channelMonitorObserverNoTargetSeconds
		}
		deadline.completeReservation(reservedUntil, nextAllowedAt)
	})
}
