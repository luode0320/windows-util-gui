//go:build bindings

// bindingGen 的绑定生成构建变体：wails 以 bindings tag 编译并执行本包生成绑定，
// 此时必须跳过自提权（见 binding_guard.go 说明）。
//
// 最近修改时间: 2026-09-13
package main

// bindingGen 绑定生成构建下为 true，main 里据此跳过自提权。
const bindingGen = true
