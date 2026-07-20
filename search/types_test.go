package search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoItemNullMetricsSerializedAsNull(t *testing.T) {
	v := VideoItem{Platform: "xiaohongshu", PostID: "p1", Title: "t"}
	data, err := json.Marshal(v)
	require.NoError(t, err)
	s := string(data)
	for _, key := range []string{`"likes":null`, `"views":null`, `"comments":null`, `"favorites":null`, `"shares":null`} {
		require.True(t, strings.Contains(s, key), "want %s in %s", key, s)
	}
	require.False(t, strings.Contains(s, "dedupe_key"))
	require.False(t, strings.Contains(s, "rank_score"))
}

func TestVideoItemNonNullMetricSerialized(t *testing.T) {
	likes := int64(42)
	data, _ := json.Marshal(VideoItem{Platform: "douyin", PostID: "p2", Likes: &likes})
	require.True(t, strings.Contains(string(data), `"likes":42`))
}
