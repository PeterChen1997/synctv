package utils_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PeterChen1997/synctv/utils"
)

func TestGetPageItems(t *testing.T) {
	type args struct {
		items    []int
		page     int
		pageSize int
	}
	tests := []struct {
		name string
		args args
		want []int
	}{
		{
			name: "Test Case 1",
			args: args{
				items:    []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
				pageSize: 5,
				page:     1,
			},
			want: []int{1, 2, 3, 4, 5},
		},
		{
			name: "Test Case 2",
			args: args{
				items:    []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
				pageSize: 5,
				page:     2,
			},
			want: []int{6, 7, 8, 9, 10},
		},
		{
			name: "Test Case 3",
			args: args{
				items:    []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
				pageSize: 5,
				page:     3,
			},
			want: []int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := utils.GetPageItems(tt.args.items, tt.args.page, tt.args.pageSize); !reflect.DeepEqual(
				got,
				tt.want,
			) {
				t.Errorf("GetPageItems() = %v, want %v", got, tt.want)
			}
		})
	}
}

func FuzzCompVersion(f *testing.F) {
	f.Add("v1.0.0", "v1.0.1")
	f.Add("v0.2.9", "v1.5.2")
	f.Add("v0.3.0-beta-1", "v0.3.0-alpha-2")
	f.Add("v0.3.1-beta.1", "v0.3.1-alpha.2")
	f.Add("v0.2.9", "v0.3.1-alpha.2")
	f.Add("v0.2.9", "v0.3.1-alpha-2")
	f.Add("v0.3.1", "v0.3.1-alpha.2")
	f.Fuzz(func(t *testing.T, a, b string) {
		t.Logf("a: %s, b: %s", a, b)
		_, err := utils.CompVersion(a, b)
		if err != nil {
			t.Errorf("CompVersion error = %v", err)
		}
	})
}

func TestIsLocalIP(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{
			name: "Test Case 1",
			host: "www.baidu.com",
			want: false,
		},
		{
			name: "Test Case 2",
			host: "127.0.0.1",
			want: true,
		},
		{
			name: "Test Case 2",
			host: "127.0.0.1:9012",
			want: true,
		},
		{
			name: "Test Case 3",
			host: "localhost:9012",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := utils.IsLocalIP(tt.host); got != tt.want {
				t.Errorf("IsLocalIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruncateByRune(t *testing.T) {
	// len("测") = 3
	name := "abcd测试"
	if !strings.EqualFold(utils.TruncateByRune(name, 6), "abcd") {
		t.Errorf("TruncateByRune() = %v, want %v", utils.TruncateByRune(name, 6), "abcd")
	}
	if !strings.EqualFold(utils.TruncateByRune(name, 7), "abcd测") {
		t.Errorf("TruncateByRune() = %v, want %v", utils.TruncateByRune(name, 7), "abcd测")
	}
	if !strings.EqualFold(utils.TruncateByRune(name, 8), "abcd测") {
		t.Errorf("TruncateByRune() = %v, want %v", utils.TruncateByRune(name, 8), "abcd测")
	}
	if !strings.EqualFold(utils.TruncateByRune(name, 9), "abcd测") {
		t.Errorf("TruncateByRune() = %v, want %v", utils.TruncateByRune(name, 9), "abcd测")
	}
	if !strings.EqualFold(utils.TruncateByRune(name, 10), "abcd测试") {
		t.Errorf("TruncateByRune() = %v, want %v", utils.TruncateByRune(name, 10), "abcd测试")
	}
}
