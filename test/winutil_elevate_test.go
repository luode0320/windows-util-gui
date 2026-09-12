package test

import (
	"os"
	"testing"

	"windows-util-gui/internal/winutil"
)

// TestShouldSkipElevate 校验跳过自提权的命令行开关识别。
//
// 该开关是调试能否正常工作的前提：识别失败会让程序在调试会话里把自己重新拉起为
// 独立的管理员进程，断点全部失效，且现象隐蔽不易排查。
//
// 最近修改时间: 2026-09-12
func TestShouldSkipElevate(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "无参数", args: []string{"app.exe"}, want: false},
		{name: "单横线写法", args: []string{"app.exe", "-no-elevate"}, want: true},
		{name: "双横线写法", args: []string{"app.exe", "--no-elevate"}, want: true},
		{name: "与其它参数共存", args: []string{"app.exe", "-verbose", "-no-elevate"}, want: true},
		{name: "无关参数不误判", args: []string{"app.exe", "-elevate"}, want: false},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			os.Args = item.args
			if got := winutil.ShouldSkipElevate(); got != item.want {
				t.Fatalf("参数 %v 的判定结果应为 %v，实际为 %v", item.args, item.want, got)
			}
		})
	}
}
