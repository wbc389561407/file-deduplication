# file-deduplication

文件去重系统，支持 Docker 部署，带 Web 控制台。

扫描多个文件夹，识别重复文件（SHA-256 内容哈希），可按**时间**或**文件夹**策略删除重复文件，删除前先移入回收站，可恢复。

## 功能特性

- **多文件夹扫描**：一次扫描多个文件夹，找出内容完全一致的重复文件
- **去重标准**：SHA-256 内容哈希一致（同一重复组内文件字节完全相同）
- **两种删除策略**
  - 按时间：每个重复组保留最新 N 份，删除其余
  - 按文件夹：指定保留文件夹，删除其它文件夹中的重复文件
- **回收站兜底**：删除的文件先移入回收站，可恢复，避免误删
- **回收站批次管理**：每次删除生成一个**批次**，记录**删除时间**与**批次号**，按批次浏览、恢复
- **Web 控制台**：目录选择、扫描进度、重复组预览、删除确认
- **目录选择器**：只允许在 **`/myfile`** 目录下选择文件夹，禁止手动输入路径
- **保留文件夹选择器**：删除策略中的"保留文件夹"也用目录选择器多选，不用手输
- **回收站映射宿主机**：回收站可映射到宿主机目录，被删除的文件在宿主机可见，便于恢复
- **三级恢复**：支持恢复**单个文件**、恢复**某个文件夹**下的全部文件、恢复**整个批次**（恢复全部）

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go + 标准库 HTTP |
| 存储 | SQLite（纯 Go 驱动 modernc.org/sqlite，无 CGO） |
| 前端 | 原生 HTML + CSS + JS（内嵌进二进制） |
| 打包 | Docker 多阶段构建（Alpine，镜像约 8MB） |

## 目录结构

```
file-deduplication/
├── main.go                 应用入口
├── Dockerfile              多阶段构建
├── docker-compose.yml      编排文件
├── internal/
│   ├── store/              SQLite 存储层
│   ├── scanner/            扫描 + SHA-256 去重
│   ├── del/                删除服务（时间/文件夹策略 + 回收站）
│   └── api/                HTTP API + 内嵌 Web 控制台
└── data/                   数据库 + 回收站（运行时生成）
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATA_DIR` | `./data` | 数据目录（SQLite 数据库 + 回收站） |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `MYFILE_ROOT` | `/myfile` | 扫描根目录（挂载点），前端只能在此目录内选择文件夹 |
| `TRASH_DIR` | `${DATA_DIR}/trash` | 回收站目录，可映射到宿主机目录便于恢复 |

## 运行方式

### 方式一：Docker Compose

```bash
docker compose up -d --build
```

`docker-compose.yml` 中把宿主机文件夹挂载到 `/myfile` 下的子目录：

```yaml
volumes:
  - /宿主机/文件夹:/myfile/你的目录名
  - filededup-data:/data
  - /宿主机/回收站目录:/trash   # 回收站映射宿主机，便于恢复
```

> 注意：挂载到 `/myfile` 的目录需要**读写**权限（删除时会先把重复文件移入回收站）。

### 方式二：docker run

```bash
docker run -d --name filededup \
  -p 8080:8080 \
  -e MYFILE_ROOT=/myfile \
  -e TRASH_DIR=/trash \
  -v /宿主机/文件夹:/myfile/你的目录名 \
  -v filededup-data:/data \
  -v /宿主机/回收站目录:/trash \
  filededup:latest
```

### 访问

浏览器打开 `http://localhost:8080`：

1. 在 Web 控制台，用**目录选择器**浏览 `/myfile` 下的文件夹，勾选需要扫描的目录
2. 点击「开始扫描」，实时查看进度
3. 扫描完成后查看重复文件分组
4. 选择删除策略（按时间/按文件夹）。按文件夹时用**目录选择器**多选要保留的文件夹，先预览再确认删除
5. 删除的文件移入回收站（映射到宿主机目录，可在宿主机直接查看/恢复），可随时清空回收站

## 重新构建本地开发

```bash
go mod tidy
go build -o filededup.exe .
# 本地调试时指定扫描根目录（模拟 /myfile）
$env:MYFILE_ROOT="C:\你的\测试目录"
$env:DATA_DIR=".\data"
$env:TRASH_DIR=".\host-trash"
$env:LISTEN_ADDR=":8080"
.\filededup.exe
```

## 安全说明

- 删除操作服务端做了路径校验，只允许操作 `/myfile` 目录下的文件
- 删除前先移入回收站，清空回收站才真正释放空间
- 受 Docker 挂载限制，容器默认只能访问已挂载的 `/myfile` 目录