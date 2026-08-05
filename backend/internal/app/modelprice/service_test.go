package modelprice

import (
	"reflect"
	"testing"
)

func TestParseImageSizePrices(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]interface{}
		want  map[string]float64
	}{
		{
			name: "quality:size 与裸 size 键混配",
			extra: map[string]interface{}{
				"image": map[string]interface{}{
					"size_prices": map[string]interface{}{
						"high:1024x1024": 0.167,
						"1024x1536":      0.06,
					},
				},
			},
			want: map[string]float64{"high:1024x1024": 0.167, "1024x1536": 0.06},
		},
		{
			name:  "无 image 段返回 nil",
			extra: map[string]interface{}{"video": map[string]interface{}{"per_second": 0.1}},
			want:  nil,
		},
		{
			name:  "image 段形态非法返回 nil 不 panic",
			extra: map[string]interface{}{"image": "bogus"},
			want:  nil,
		},
		{
			name:  "size_prices 形态非法返回 nil 不 panic",
			extra: map[string]interface{}{"image": map[string]interface{}{"size_prices": []interface{}{1, 2}}},
			want:  nil,
		},
		{
			name: "<=0 与非数值条目丢弃，全丢弃返回 nil",
			extra: map[string]interface{}{
				"image": map[string]interface{}{
					"size_prices": map[string]interface{}{
						"low:512x512":  0.0,
						"high:512x512": -1,
						"mid:512x512":  "abc",
					},
				},
			},
			want: nil,
		},
		{
			name:  "空 extra 返回 nil",
			extra: nil,
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseImageSizePrices("gpt-image-2", tc.extra)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseImageSizePrices() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseVideoResolutionPrices(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]interface{}
		want  map[string]float64
	}{
		{
			name: "解析并规范化分辨率键",
			extra: map[string]interface{}{
				"video": map[string]interface{}{
					"resolution_prices": map[string]interface{}{
						" 480P ": 0.05,
						"720p":   0.07,
					},
				},
			},
			want: map[string]float64{"480p": 0.05, "720p": 0.07},
		},
		{
			name: "非法和非正价格被丢弃",
			extra: map[string]interface{}{
				"video": map[string]interface{}{
					"resolution_prices": map[string]interface{}{"480p": 0, "720p": "bad"},
				},
			},
			want: nil,
		},
		{name: "缺少视频扩展", extra: nil, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseVideoResolutionPrices("video-model", tc.extra)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseVideoResolutionPrices() = %v, want %v", got, tc.want)
			}
		})
	}
}
