package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"filededup/internal/store"
)

// ProgressCb 进度回调，done 为已完成文件数，total 为总文件数
type ProgressCb func(done, total int, phase string)

// Scanner 扫描并计算文件哈希
type Scanner struct {
	store *store.Store
}

func New(st *store.Store) *Scanner {
	return &Scanner{store: st}
}

// Run 扫描所有已配置文件夹，识别重复文件
func (s *Scanner) Run(ctx context.Context, cb ProgressCb) error {
	folders, err := s.store.ListFolders()
	if err != nil {
		return err
	}
	if len(folders) == 0 {
		if cb != nil {
			cb(0, 1, "没有配置文件夹")
		}
		return nil
	}

	// 阶段一：收集文件清单（按大小分组）
	type fileInfo struct {
		Path     string
		Size     int64
		ModTime  int64
		FolderID int64
	}
	bySize := map[int64][]fileInfo{}
	var all []fileInfo
	var total int
	for _, f := range folders {
		err := filepath.WalkDir(f.Path, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return nil // 跳过无法访问的条目
			}
			if d.IsDir() {
				return nil
			}
			info, err2 := d.Info()
			if err2 != nil {
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rec := fileInfo{Path: path, Size: info.Size(), ModTime: info.ModTime().Unix(), FolderID: f.ID}
			bySize[info.Size()] = append(bySize[info.Size()], rec)
			all = append(all, rec)
			total++
			return nil
		})
		if err != nil {
			return err
		}
	}

	// 阶段二：仅对同大小的文件计算哈希
	candidate := make([]fileInfo, 0)
	for size, list := range bySize {
		if len(list) > 1 {
			_ = size
			candidate = append(candidate, list...)
		}
	}

	results := make([]store.FileRecord, 0, len(all))
	done := 0
	doneMu := sync.Mutex{}

	hashes := make([]string, len(candidate))
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				h := hashFile(candidate[idx].Path)
				hashes[idx] = h
				doneMu.Lock()
				done++
				d := done
				doneMu.Unlock()
				if cb != nil && d%200 == 0 {
					cb(d, total, "哈希计算")
				}
			}
		}()
	}
	for i := range candidate {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		default:
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// 组装文件记录
	for i, c := range candidate {
		results = append(results, store.FileRecord{
			Path:     c.Path,
			Size:     c.Size,
			Hash:     hashes[i],
			ModTime:  c.ModTime,
			FolderID: c.FolderID,
		})
	}
	// 非候选（唯一大小）文件也入库，哈希留空
	for _, list := range bySize {
		if len(list) == 1 {
			for _, it := range list {
				results = append(results, store.FileRecord{
					Path: it.Path, Size: it.Size, Hash: "", ModTime: it.ModTime, FolderID: it.FolderID,
				})
			}
		}
	}

	if err := s.store.ClearDeleted(); err != nil {
		return err
	}
	if err := s.store.SaveFiles(results); err != nil {
		return err
	}
	if cb != nil {
		cb(total, total, "完成")
	}
	return nil
}

func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}