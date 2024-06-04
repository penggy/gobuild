// Copyright 2015 by caixw, All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/penggy/go-cache"
)

type builder struct {
	exts           []string  // 需要监视的文件扩展名
	appName        string    // 输出的程序文件
	appCmd         *exec.Cmd // appName 的命令行包装引用，方便结束其进程。
	appArgs        []string  // 传递给 appCmd 的参数
	goCmdArgs      []string  // 传递给 go build 的参数
	delaySeconds   uint
	coolingSeconds uint
	cache          *cache.Cache // appName <-> appName
	watcher        *fsnotify.Watcher
	buildTime      time.Time
	wg             sync.WaitGroup
}

// 确定文件 path 是否属于被忽略的格式。
func (b *builder) isIgnore(path string) bool {
	if b.appCmd != nil && b.appCmd.Path == path { // 忽略程序本身的监视
		return true
	}

	for _, ext := range b.exts {
		if ext == "*" {
			return false
		}
		if strings.HasSuffix(path, ext) {
			return false
		}
	}

	return true
}

// 开始编译代码
func (b *builder) build() {
	b.buildTime = time.Now()
	info.Println("编译代码...")

	goCmd := exec.Command("go", b.goCmdArgs...)
	goCmd.Stderr = os.Stderr
	goCmd.Stdout = os.Stdout
	if err := goCmd.Run(); err != nil {
		b.buildTime = time.Now()
		erro.Println("编译失败:", err)
		return
	}
	b.buildTime = time.Now()
	succ.Println("编译成功!")

	b.restart()
	b.buildTime = time.Now()
}

// 重启被编译的程序
func (b *builder) restart() {
	defer func() {
		if err := recover(); err != nil {
			erro.Println("restart.defer:", err)
		}
	}()

	// kill process
	if b.appCmd != nil && b.appCmd.Process != nil {
		info.Println("中止旧进程:", b.appName)
		if err := b.appCmd.Process.Kill(); err != nil {
			erro.Println("kill:", err)
		}
		succ.Println("旧进程被终止!")
	}

	info.Println("启动新进程:", b.appName)
	b.appCmd = exec.Command(b.appName, b.appArgs...)
	b.appCmd.Dir = filepath.Dir(b.appName) // 确定程序的工作目录
	b.appCmd.Stderr = os.Stderr
	b.appCmd.Stdout = os.Stdout
	if err := b.appCmd.Start(); err != nil {
		erro.Println("启动进程时出错:", err)
	}
}

// 过滤掉不需要监视的目录。以下目录会被过滤掉：
// 整个目录下都没需要监视的文件；
func (b *builder) filterPaths(paths []string) []string {
	ret := make([]string, 0, len(paths))

	for _, dir := range paths {
		fs, err := os.ReadDir(dir)
		if err != nil {
			erro.Println(err)
			continue
		}

		ignore := true
		for _, f := range fs {
			if f.IsDir() {
				continue
			}
			if !b.isIgnore(f.Name()) {
				ignore = false
				break
			}
		}
		if !ignore {
			ret = append(ret, dir)
		}
	} // end for paths

	return ret
}

func (b *builder) initWatcher(paths []string) (err error) {
	info.Println("初始化监视器...")

	// 初始化监视器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}

	paths = b.filterPaths(paths)

	// info.Println("以下路径或是文件将被监视:")
	// for _, path := range paths {
	// 	info.Println(path)
	// }

	for _, path := range paths {
		err = watcher.Add(path)
		if err != nil {
			watcher.Close()
			return
		}
	}
	b.watcher = watcher
	return
}

func (b *builder) triggerBuild() {
	if b.cache != nil {
		b.cache.SetDefault(b.appName, b.appName)
	} else {
		b.buildTime = time.Now()
		go b.build()
	}
}

// 开始监视 paths 中指定的目录或文件。
func (b *builder) watch() {
	defer b.wg.Done()
	defer b.watcher.Close()
	for {
		select {
		case event := <-b.watcher.Events:
			if event.Op&fsnotify.Chmod == fsnotify.Chmod {
				ignore.Println("watcher.Events:忽略 CHMOD 事件:", event)
				continue
			}

			if b.isIgnore(event.Name) { // 不需要监视的扩展名
				ignore.Println("watcher.Events:忽略不被监视的文件:", event)
				continue
			}

			if time.Since(b.buildTime) <= time.Duration(b.coolingSeconds)*time.Second { // 冷却期
				ignore.Println("watcher.Events:该监控事件被忽略:", event)
				continue
			}

			if b.cache != nil && b.cache.ItemCount() > 0 { // 事件已存在
				ignore.Println("watcher.Events:该监控事件被忽略:", event)
				continue
			}

			info.Println("watcher.Events:触发编译事件:", event)

			// go b.build()
			b.triggerBuild()
		case err := <-b.watcher.Errors:
			// watcher.Close()
			warn.Println("watcher.Errors", err)
			return
		} // end select
	}
}
