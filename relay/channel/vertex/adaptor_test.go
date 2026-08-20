package vertex

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newImagenConversionFixture() (*Adaptor, *gin.Context, *common.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return &Adaptor{RequestMode: RequestModeGemini}, c, &common.RelayInfo{
		OriginModelName:   "imagen-3.0-generate-002",
		UpstreamModelName: "imagen-3.0-generate-002",
	}
}

func TestConvertOpenAIRequestValidatesImagenTopLevelN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, n := range []int{0, -1, dto.MaxImageN + 1} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			adaptor, c, info := newImagenConversionFixture()
			_, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
				Model:  info.UpstreamModelName,
				Prompt: "mountain",
				N:      lo.ToPtr(n),
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "n must be an integer")
		})
	}
}

func TestConvertOpenAIRequestValidatesImagenExtraBodyN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range map[string]string{
		"zero":       `{"n":0}`,
		"negative":   `{"n":-1}`,
		"fractional": `{"n":1.5}`,
		"string":     `{"n":"2"}`,
		"too large":  fmt.Sprintf(`{"n":%d}`, dto.MaxImageN+1),
	} {
		t.Run(name, func(t *testing.T) {
			adaptor, c, info := newImagenConversionFixture()
			_, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
				Model:     info.UpstreamModelName,
				Prompt:    "mountain",
				ExtraBody: []byte(body),
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "extra_body.n must be an integer")
		})
	}
}

func TestConvertOpenAIRequestAcceptsImagenNBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, n := range []int{1, dto.MaxImageN} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			adaptor, c, info := newImagenConversionFixture()
			converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
				Model:  info.UpstreamModelName,
				Prompt: "mountain",
				N:      lo.ToPtr(n),
			})
			require.NoError(t, err)
			request, ok := converted.(dto.GeminiImageRequest)
			require.True(t, ok)
			assert.Equal(t, n, request.Parameters.SampleCount)
		})
	}
}
