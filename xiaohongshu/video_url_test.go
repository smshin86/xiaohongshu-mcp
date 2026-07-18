package xiaohongshu

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetailVideo_VideoURL(t *testing.T) {
	tests := []struct {
		name  string
		video *DetailVideo
		want  string
	}{
		{
			name:  "nil 수신자는 빈 문자열",
			video: nil,
			want:  "",
		},
		{
			name:  "stream 없으면 빈 문자열",
			video: &DetailVideo{},
			want:  "",
		},
		{
			name: "h264 우선",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h264": {{MasterURL: "https://h264.mp4"}},
				"h265": {{MasterURL: "https://h265.mp4"}},
			}}},
			want: "https://h264.mp4",
		},
		{
			name: "h265 폴백",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h265": {{MasterURL: "https://h265.mp4"}},
				"av1":  {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://h265.mp4",
		},
		{
			name: "av1 폴백",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"av1": {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://av1.mp4",
		},
		{
			name: "빈 masterUrl 건너뜀",
			video: &DetailVideo{Media: DetailMedia{Stream: map[string][]DetailStreamItem{
				"h264": {{MasterURL: ""}},
				"av1":  {{MasterURL: "https://av1.mp4"}},
			}}},
			want: "https://av1.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.video.VideoURL())
		})
	}
}

// TestFeedDetailVideoUnmarshal 은 상세 페이지 JSON 구조(note.video.media.stream)와
// 구조체 매핑이 일치함을 고정한다.
func TestFeedDetailVideoUnmarshal(t *testing.T) {
	raw := `{"video":{"media":{"stream":{"h264":[{"masterUrl":"https://x.mp4"}]}}}}`
	var d FeedDetail
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	require.NotNil(t, d.Video)
	require.Equal(t, "https://x.mp4", d.Video.VideoURL())
}
