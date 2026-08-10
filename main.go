package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"filededup/internal/api"
	"filededup/internal/store"
)

// version 当前版本号
const version = "v1.0.2"

func main() {
	dataDir := getenv("DATA_DIR", "./data")
	addr := getenv("LISTEN_ADDR", ":8080")
	// 扫描根目录（挂载点），前端只能在此目录下选择文件夹
	myfileRoot := getenv("MYFILE_ROOT", "/myfile")
	// 回收站目录（可映射到宿主机目录，便于恢复）
	trashDir := getenv("TRASH_DIR", filepath.Join(dataDir, "trash"))

	st, err := store.Open(dataDir)
	if err != nil {
		log.Fatalf("初始化存储失败: %v", err)
	}
	defer st.Close()

	srv := api.New(st, dataDir, myfileRoot, trashDir, version)
	log.Printf("版本: %s", version)
	log.Printf("数据目录: %s", mustAbs(dataDir))
	log.Printf("回收站: %s", mustAbs(trashDir))
	log.Printf("扫描根目录: %s", myfileRoot)
	log.Printf("监听: %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}