const $ = s => document.querySelector(s);
const fmt = n => n >= 1073741824 ? (n/1073741824).toFixed(2)+' GB'
  : n >= 1048576 ? (n/1048576).toFixed(2)+' MB'
  : n >= 1024 ? (n/1024).toFixed(1)+' KB' : n+' B';
const fmtTime = t => t ? new Date(t*1000).toLocaleString() : '';

async function api(url, method='GET', body) {
  const opt = { method, headers: {} };
  if (body) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch(url, opt);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

// folders：目录选择器（只能在 /myfile 下选择）
let myfileRoot = '/myfile';
let currentDir = '/myfile';

async function loadFolders() {
  const folders = await api('/api/folders');
  $('#folderList').innerHTML = folders.map(f =>
    `<li><span>${f.path}</span><button class="del" data-id="${f.id}">删除</button></li>`).join('');
  document.querySelectorAll('#folderList .del').forEach(b =>
    b.onclick = async () => { await api('/api/folders/'+b.dataset.id, 'DELETE'); loadFolders(); });
}

async function loadDir() {
  const res = await api('/api/myfile/list?path=' + encodeURIComponent(currentDir));
  currentDir = res.path;
  myfileRoot = res.root;
  $('#currentPath').textContent = currentDir === res.root ? res.root + '（根）' : res.path;
  $('#upBtn').disabled = (res.path === res.root);
  const dirs = res.dirs || [];
  $('#dirList').innerHTML = dirs.length
    ? dirs.map(d => `<li data-path="${d.path}"><span class="dir-icon">📁</span>${d.name}</li>`).join('')
    : '<li class="empty">（该目录下没有子文件夹）</li>';
  document.querySelectorAll('#dirList li[data-path]').forEach(li =>
    li.onclick = () => { currentDir = li.dataset.path; loadDir(); });
}

async function initBrowser() {
  const root = await api('/api/myfile/root');
  if (!root.exists) {
    $('#dirList').innerHTML = `<li class="empty">扫描根目录 ${root.root} 不存在，请先在 docker 中挂载目录</li>`;
    $('#currentPath').textContent = root.root;
    $('#addFolderBtn').disabled = true;
    return;
  }
  myfileRoot = root.root;
  currentDir = root.root;
  loadDir();
}

$('#upBtn').onclick = () => {
  if (currentDir === myfileRoot) return;
  const idx = currentDir.lastIndexOf('/');
  currentDir = idx > myfileRoot.length ? currentDir.slice(0, idx) : myfileRoot;
  loadDir();
};
$('#addFolderBtn').onclick = async () => {
  await api('/api/folders', 'POST', { path: currentDir });
  loadFolders();
};

// scan
let pollTimer = null;
async function startScan() {
  $('#taskStatus').textContent = '启动…';
  await api('/api/scan', 'POST');
  $('#scanBtn').disabled = true; $('#cancelBtn').disabled = false;
  pollTimer = setInterval(pollTask, 800);
}
async function pollTask() {
  const t = await api('/api/task');
  if (t.status === 'running') {
    $('#taskStatus').textContent = (t.message||'') + ' ' + t.progress + '%';
    $('#progressBar').style.width = t.progress + '%';
  } else {
    clearInterval(pollTimer);
    $('#scanBtn').disabled = false; $('#cancelBtn').disabled = true;
    $('#taskStatus').textContent = t.status === 'done' ? '扫描完成' : (t.status === 'error' ? '失败: '+t.message : '空闲');
    if (t.status === 'done') loadDups();
  }
}
$('#scanBtn').onclick = startScan;
$('#cancelBtn').onclick = async () => { await api('/api/scan/cancel', 'POST'); };

// dups
async function loadDups() {
  const groups = await api('/api/dups');
  let totalSeize = 0, totalFiles = 0;
  $('#dupList').innerHTML = groups.map(g => {
    totalSeize += g.reclaimable; totalFiles += g.file_count - 1;
    return `<div class="group">
      <div class="group-head"><span>哈希 <b>${g.hash.slice(0,12)}…</b> · ${g.file_count} 个文件 · 单个 ${fmt(g.size)} · 可释放 <b>${fmt(g.reclaimable)}</b></span></div>
      <div class="group-files">${g.files.map(f => `<div class="file-row"><span class="path">${f.path}</span><span class="time">${fmtTime(f.mod_time)}</span></div>`).join('')}</div>
    </div>`;
  }).join('');
  $('#dupSummary').textContent = groups.length ? `共 ${groups.length} 组，可释放 ${fmt(totalSeize)}，去重 ${totalFiles} 个文件` : '（无重复）';
}

// strategy
document.querySelectorAll('input[name=mode]').forEach(r =>
  r.onchange = () => {
    $('#timeOpts').classList.toggle('hidden', r.value !== 'time');
    $('#folderOpts').classList.toggle('hidden', r.value !== 'folder');
  });

function strategy() {
  const mode = document.querySelector('input[name=mode]:checked').value;
  return {
    mode,
    keep_n: mode === 'time' ? parseInt($('#keepN').value) || 1 : 0,
    keep_folders: mode === 'folder' ? $('#keepFolder').value.split(/[,，]/).map(s=>s.trim()).filter(Boolean) : []
  };
}
$('#previewBtn').onclick = async () => {
  const files = await api('/api/delete/preview', 'POST', strategy());
  $('#previewResult').textContent = files.length
    ? `将删除 ${files.length} 个文件：\n` + files.map(f => f.path).join('\n')
    : '没有可删除的文件';
};
$('#deleteBtn').onclick = async () => {
  if (!confirm('确认执行删除？文件将移入回收站。')) return;
  const res = await api('/api/delete', 'POST', strategy());
  alert(`已删除 ${res.count} 个文件（已移入回收站）`);
  loadDups(); loadTrash();
};

// trash
async function loadTrash() {
  const t = await api('/api/trash');
  $('#trashInfo').textContent = `${t.count} 个文件 · ${t.dir}`;
}
$('#emptyTrashBtn').onclick = async () => {
  if (!confirm('确认清空回收站？此操作不可恢复！')) return;
  await api('/api/trash/empty', 'POST');
  loadTrash();
};

loadFolders(); initBrowser(); loadDups(); loadTrash();