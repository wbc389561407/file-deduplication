package store

// Folder 待扫描文件夹配置
type Folder struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	CreatedAt int64  `json:"created_at"`
}

// FileRecord 文件记录
type FileRecord struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Hash     string `json:"hash"`
	ModTime  int64  `json:"mod_time"`
	FolderID int64  `json:"folder_id"`
	Deleted  int    `json:"deleted"`
}

// DupGroup 重复组（由 files 表按 hash 聚合计算）
type DupGroup struct {
	Hash      string `json:"hash"`
	Size      int64  `json:"size"` // 单个文件大小
	FileCount int    `json:"file_count"`
	TotalSize int64  `json:"total_size"`
	// 可释放空间 = (file_count-1) * size
	Reclaimable int64        `json:"reclaimable"`
	Files       []FileRecord `json:"files"`
}

// Task 扫描任务状态
type Task struct {
	ID         int64  `json:"id"`
	Status     string `json:"status"` // idle / running / done / error
	Progress   int    `json:"progress"`
	Message    string `json:"message"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}