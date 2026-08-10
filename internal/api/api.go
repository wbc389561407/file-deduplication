package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"filededup/internal/del"
	"filededup/internal/scanner"
	"filededup/internal/store"
)

var (
	//go:embed web/*
	webFS embed.FS
)

// Server HTTP 服务
type Server struct {
	store   *store.Store
	scanner *scanner.Scanner
	del     *del.Service
	dataDir string
	myfile  string // 扫描根目录（挂载点），前端只能在此目录内选择
	version string

	mu     sync.Mutex
	task   *store.Task
	cancel context.CancelFunc
}

func New(st *store.Store, dataDir string, myfileRoot string, trashDir string, version string) *Server {
	// 扫描根目录统一转为绝对路径，保证与入库的绝对文件路径一致（inMyfile / relPath 校验才有效）
	myfileAbs, err := filepath.Abs(myfileRoot)
	if err != nil {
		myfileAbs = filepath.Clean(myfileRoot)
	}
	return &Server{
		store:   st,
		scanner: scanner.New(st),
		del:     del.New(st, trashDir, myfileAbs),
		dataDir: dataDir,
		myfile:  myfileAbs,
		version: version,
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/folders", s.listFolders)
	mux.HandleFunc("GET /api/version", s.versionInfo)
	mux.HandleFunc("POST /api/folders", s.addFolder)
	mux.HandleFunc("DELETE /api/folders/{id}", s.deleteFolder)
	mux.HandleFunc("GET /api/myfile/root", s.myfileRoot)
	mux.HandleFunc("GET /api/myfile/list", s.myfileList)
	mux.HandleFunc("GET /api/preview", s.previewFile)
	mux.HandleFunc("POST /api/scan", s.startScan)
	mux.HandleFunc("POST /api/scan/cancel", s.cancelScan)
	mux.HandleFunc("GET /api/task", s.getTask)
	mux.HandleFunc("GET /api/dups", s.listDups)
	mux.HandleFunc("POST /api/delete/preview", s.deletePreview)
	mux.HandleFunc("POST /api/delete", s.deleteExecute)
	mux.HandleFunc("GET /api/trash", s.trashInfo)
	mux.HandleFunc("GET /api/trash/batches", s.trashBatches)
	mux.HandleFunc("GET /api/trash/batches/{batch}", s.trashBatchFiles)
	mux.HandleFunc("POST /api/trash/restore", s.trashRestore)
	mux.HandleFunc("POST /api/trash/empty", s.emptyTrash)

	// 静态前端
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	return logMW(mux)
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("[%s] %s %s %s\n", time.Now().Format("15:04:05"), r.Method, r.URL.Path, time.Since(start))
	})
}

// versionInfo 返回当前版本号
func (s *Server) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"version": s.version})
}

// ---------- folders ----------

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	fs, err := s.store.ListFolders()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, fs)
}

// inMyfile 校验路径是否位于扫描根目录 /myfile 之下
func (s *Server) inMyfile(path string) bool {
	p := filepath.Clean(path)
	if p == s.myfile {
		return true
	}
	return strings.HasPrefix(p+string(filepath.Separator), s.myfile+string(filepath.Separator))
}

func (s *Server) addFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeErr(w, 400, "路径不能为空")
		return
	}
	if !s.inMyfile(body.Path) {
		writeErr(w, 400, "只能选择 "+s.myfile+" 目录下的文件夹")
		return
	}
	if err := s.store.AddFolder(body.Path); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "true"})
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.store.DeleteFolder(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "true"})
}

// ---------- /myfile 目录浏览 ----------

// myfileRoot 返回扫描根目录信息
func (s *Server) myfileRoot(w http.ResponseWriter, r *http.Request) {
	info, err := os.Stat(s.myfile)
	exists := err == nil && info.IsDir()
	writeJSON(w, 200, map[string]any{"root": s.myfile, "exists": exists})
}

// myfileList 列出指定目录（须在 /myfile 下）里的子目录
func (s *Server) myfileList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.myfile
	}
	if !s.inMyfile(path) {
		writeErr(w, 400, "只能浏览 "+s.myfile+" 目录下的内容")
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var dirs []map[string]string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, map[string]string{
			"name": e.Name(),
			"path": filepath.Join(path, e.Name()),
		})
	}
	writeJSON(w, 200, map[string]any{"path": filepath.Clean(path), "root": s.myfile, "dirs": dirs})
}

// previewType 根据扩展名返回可预览的 MIME 类型；不可预览时 ok 为 false
func previewType(path string) (mimeType string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	// 图片
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".svg":
		return "image/svg+xml", true
	case ".bmp":
		return "image/bmp", true
	case ".ico":
		return "image/x-icon", true
	// 文本
	case ".txt", ".log", ".ini", ".conf", ".cfg", ".sh", ".bat", ".py", ".go", ".java", ".c", ".cpp", ".h", ".js", ".ts", ".tsx", ".jsx", ".css", ".md", ".json", ".xml", ".yaml", ".yml", ".csv", ".html", ".htm", ".sql":
		return "text/plain; charset=utf-8", true
	// PDF
	case ".pdf":
		return "application/pdf", true
	// 音频
	case ".mp3":
		return "audio/mpeg", true
	case ".wav":
		return "audio/wav", true
	case ".ogg":
		return "audio/ogg", true
	case ".flac":
		return "audio/flac", true
	// 视频
	case ".mp4":
		return "video/mp4", true
	case ".webm":
		return "video/webm", true
	case ".ogv":
		return "video/ogg", true
	}
	return "", false
}

// previewFile 内联返回文件内容，供浏览器预览（仅 /myfile 下、可预览类型）
// 传入 batch 时，从回收站对应路径返回（用于预览已删除的文件）
func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	batch := r.URL.Query().Get("batch")
	if path == "" {
		writeErr(w, 400, "缺少 path 参数")
		return
	}
	if !s.inMyfile(path) {
		writeErr(w, 400, "只能预览 "+s.myfile+" 目录下的文件")
		return
	}
	mimeType, ok := previewType(path)
	if !ok {
		writeErr(w, 400, "该文件类型不支持预览")
		return
	}
	serve := path
	if batch != "" {
		serve = s.del.TrashPath(batch, path)
	}
	info, err := os.Stat(serve)
	if err != nil {
		writeErr(w, 404, "文件不存在")
		return
	}
	if info.IsDir() {
		writeErr(w, 400, "不能预览目录")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.QueryEscape(filepath.Base(path)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, serve)
}

// ---------- scan ----------

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task != nil && s.task.Status == "running" {
		writeErr(w, 409, "扫描正在进行中")
		return
	}
	id, err := s.store.CreateTask()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	task, _ := s.store.GetTask(id)
	ctx, cancel := context.WithCancel(context.Background())
	s.task = task
	s.cancel = cancel

	go func() {
		err := s.scanner.Run(ctx, func(done, total int, phase string) {
			p := 0
			if total > 0 {
				p = done * 100 / total
			}
			_ = s.store.UpdateTask(id, "running", p, phase)
			s.mu.Lock()
			s.task.Progress = p
			s.task.Message = phase
			s.mu.Unlock()
		})
		s.mu.Lock()
		s.task = nil
		s.cancel = nil
		s.mu.Unlock()
		if err != nil && err != context.Canceled {
			_ = s.store.FinishTask(id, "error")
			_ = s.store.UpdateTask(id, "error", 0, err.Error())
		} else {
			_ = s.store.FinishTask(id, "done")
		}
	}()
	writeJSON(w, 200, task)
}

func (s *Server) cancelScan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	writeJSON(w, 200, map[string]string{"ok": "true"})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cur := s.task
	s.mu.Unlock()
	if cur != nil {
		writeJSON(w, 200, cur)
		return
	}
	t, err := s.store.LatestTask()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if t == nil {
		writeJSON(w, 200, map[string]string{"status": "idle"})
		return
	}
	writeJSON(w, 200, t)
}

// ---------- dups ----------

func (s *Server) listDups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListDupGroups()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, groups)
}

// ---------- delete ----------

// validateStrategy 校验删除策略：保留文件夹必须位于 /myfile 下
func (s *Server) validateStrategy(str *del.Strategy) error {
	if str.Mode == "folder" {
		if len(str.KeepFolders) == 0 {
			return fmt.Errorf("请选择至少一个保留文件夹")
		}
		for _, p := range str.KeepFolders {
			if !s.inMyfile(p) {
				return fmt.Errorf("保留文件夹 %s 不在 %s 目录下", p, s.myfile)
			}
		}
	}
	return nil
}

func (s *Server) deletePreview(w http.ResponseWriter, r *http.Request) {
	var str del.Strategy
	if err := json.NewDecoder(r.Body).Decode(&str); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if err := s.validateStrategy(&str); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	files, err := s.del.Preview(str)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, files)
}

func (s *Server) deleteExecute(w http.ResponseWriter, r *http.Request) {
	var str del.Strategy
	if err := json.NewDecoder(r.Body).Decode(&str); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if err := s.validateStrategy(&str); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	files, err := s.del.Execute(str)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": files, "count": len(files)})
}

// ---------- trash ----------

func (s *Server) trashInfo(w http.ResponseWriter, r *http.Request) {
	n, err := s.del.TrashCount()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"count": n, "dir": s.del.TrashDir()})
}

// trashBatches 返回回收站批次列表（含删除时间）
func (s *Server) trashBatches(w http.ResponseWriter, r *http.Request) {
	batches, err := s.store.TrashBatches()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, batches)
}

// trashBatchFiles 返回某批次（可按文件夹过滤）内的已删除文件
func (s *Server) trashBatchFiles(w http.ResponseWriter, r *http.Request) {
	batch := r.PathValue("batch")
	folder := r.URL.Query().Get("folder")
	if folder != "" && !s.inMyfile(folder) {
		writeErr(w, 400, "只能浏览 "+s.myfile+" 目录下的内容")
		return
	}
	files, err := s.store.TrashFiles(batch, folder)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, files)
}

// trashRestore 恢复指定文件（或指定文件夹下所有文件），paths 为原始路径
func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Batch string   `json:"batch"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if strings.TrimSpace(body.Batch) == "" || len(body.Paths) == 0 {
		writeErr(w, 400, "缺少批次或文件路径")
		return
	}
	for _, p := range body.Paths {
		if !s.inMyfile(p) {
			writeErr(w, 400, "文件路径 "+p+" 不在 "+s.myfile+" 目录下")
			return
		}
	}
	n, err := s.del.Restore(body.Batch, body.Paths)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"count": n})
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request) {
	if err := s.del.EmptyTrash(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "true"})
}