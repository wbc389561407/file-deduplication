package del

import (
	"os"
	"path/filepath"
	"strings"

	"filededup/internal/store"
)

// Strategy 删除策略
type Strategy struct {
	Mode       string   `json:"mode"`        // "time" | "folder"
	KeepN      int      `json:"keep_n"`      // time 模式保留份数，默认 1
	KeepFolders []string `json:"keep_folders"` // folder 模式保留的文件夹
	Hashes     []string `json:"hashes"`      // 指定重复组 hash，为空表示全部
}

// Service 删除服务
type Service struct {
	store *store.Store
	trash string
}

func New(st *store.Store, dataDir string) *Service {
	return &Service{store: st, trash: filepath.Join(dataDir, "trash")}
}

// Preview 预览将要删除的文件，不真正执行
func (s *Service) Preview(str Strategy) ([]store.FileRecord, error) {
	return s.decide(str)
}

// Execute 执行删除：将文件移入回收站
func (s *Service) Execute(str Strategy) ([]store.FileRecord, error) {
	toDelete, err := s.decide(str)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.trash, 0o755); err != nil {
		return nil, err
	}
	var deleted []store.FileRecord
	for _, f := range toDelete {
		dest := filepath.Join(s.trash, f.Hash, filepath.Base(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			continue
		}
		if err := os.Rename(f.Path, dest); err != nil {
			// 跨盘或失败时改为复制+删除
			if err2 := copyAndRemove(f.Path, dest); err2 != nil {
				continue
			}
		}
		if err := s.store.MarkDeleted(f.Path); err != nil {
			continue
		}
		deleted = append(deleted, f)
	}
	return deleted, nil
}

// decide 依据策略计算要删除的文件
func (s *Service) decide(str Strategy) ([]store.FileRecord, error) {
	groups, err := s.store.ListDupGroups()
	if err != nil {
		return nil, err
	}
	hashSet := map[string]bool{}
	for _, h := range str.Hashes {
		hashSet[h] = true
	}
	var out []store.FileRecord
	for _, g := range groups {
		if len(hashSet) > 0 && !hashSet[g.Hash] {
			continue
		}
		switch str.Mode {
		case "folder":
			keep := map[string]bool{}
			for _, k := range str.KeepFolders {
				keep[strings.TrimRight(k, `/\`)] = true
			}
			for _, f := range g.Files {
				if !inKeepFolders(f.Path, keep) {
					out = append(out, f)
				}
			}
		case "time":
			keepN := str.KeepN
			if keepN < 1 {
				keepN = 1
			}
			// g.Files 已按 mod_time 降序，保留前 keepN 份
			for i := keepN; i < len(g.Files); i++ {
				out = append(out, g.Files[i])
			}
		default:
			// 默认按时间保留最新 1 份
			for i := 1; i < len(g.Files); i++ {
				out = append(out, g.Files[i])
			}
		}
	}
	return out, nil
}

func inKeepFolders(path string, keep map[string]bool) bool {
	dir := filepath.Dir(path)
	for k := range keep {
		abs, err := filepath.Abs(k)
		if err != nil {
			continue
		}
		// 文件所在目录本身或某上级目录与保留文件夹匹配
		if strings.HasPrefix(dir+string(filepath.Separator), abs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func copyAndRemove(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return os.Remove(src)
}

// TrashCount 回收站占用
func (s *Service) TrashCount() (int, error) {
	n := 0
	err := filepath.WalkDir(s.trash, func(p string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

// EmptyTrash 清空回收站
func (s *Service) EmptyTrash() error {
	return os.RemoveAll(s.trash)
}

func (s *Service) TrashDir() string { return s.trash }