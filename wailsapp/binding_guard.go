//go:build !bindings

// bindingGen 标记当前是否处于 wails 绑定生成构建（bindings tag）。
//
// wails build 生成绑定时会以 bindings tag 编译并执行本包一次，
// 该场景下禁止触发自提权，否则构建会被 UAC 弹窗卡死。
//
// 最近修改时间: 2026-09-13
package main

// bindingGen 正常构建下为 false，自提权按需执行。
const bindingGen = false
