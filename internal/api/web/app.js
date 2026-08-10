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
  const folders = (await api('/api/folders')) || [];
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

// 通用目录选择弹窗（用于“保留文件夹”等场景）
let pickerCb = null;
let pickerRoot = '/myfile';
let pickerDir = '/myfile';

function openDirPicker(cb) {
  pickerCb = cb;
  $('#dirPicker').classList.remove('hidden');
  const root = api('/api/myfile/root').then(r => {
    pickerRoot = r.root; pickerDir = r.root;
    loadPicker(r.root);
  });
}
async function loadPicker(path) {
  const res = await api('/api/myfile/list?path=' + encodeURIComponent(path));
  pickerDir = res.path; pickerRoot = res.root;
  $('#pickerPath').textContent = res.path === res.root ? res.root + ' （根）' : res.path;
  $('#pickerUp').disabled = (res.path === res.root);
  const dirs = res.dirs || [];
  $('#pickerList').innerHTML = dirs.length
    ? dirs.map(d => `<li data-path="${d.path}"><span class="dir-icon">📁</span>${d.name}</li>`).join('')
    : '<li class="empty">（该目录下没有子文件夹）</li>';
  document.querySelectorAll('#pickerList li[data-path]').forEach(li =>
    li.onclick = () => loadPicker(li.dataset.path));
}
$('#pickerUp').onclick = () => {
  if (pickerDir === pickerRoot) return;
  const idx = pickerDir.lastIndexOf('/');
  pickerDir = idx > pickerRoot.length ? pickerDir.slice(0, idx) : pickerRoot;
  loadPicker(pickerDir);
};
$('#pickerAdd').onclick = () => { $('#dirPicker').classList.add('hidden'); if (pickerCb) pickerCb(pickerDir); };
$('#pickerCancel').onclick = () => { $('#dirPicker').classList.add('hidden'); };

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
  const groups = (await api('/api/dups')) || [];
  let totalSeize = 0, totalFiles = 0;
  $('#dupList').innerHTML = groups.map(g => {
    totalSeize += g.reclaimable; totalFiles += g.file_count - 1;
    return `<div class="group">
      <div class="group-head"><span>哈希 <b>${g.hash.slice(0,12)}…</b> · ${g.file_count} 个文件 · 单个 ${fmt(g.size)} · 可释放 <b>${fmt(g.reclaimable)}</b></span></div>
      <div class="group-files">${g.files.map(f => `<div class="file-row"><span class="path">${f.path}</span>${previewBtn(f.path)}<span class="time">${fmtTime(f.mod_time)}</span></div>`).join('')}</div>
    </div>`;
  }).join('');
  $('#dupSummary').textContent = groups.length ? `共 ${groups.length} 组，可释放 ${fmt(totalSeize)}，去重 ${totalFiles} 个文件` : '（无重复）';
  document.querySelectorAll('#dupList .preview-file').forEach(b =>
    b.onclick = () => openPreview(decodeURIComponent(b.dataset.path)));
}

// strategy
document.querySelectorAll('input[name=mode]').forEach(r =>
  r.onchange = () => {
    $('#timeOpts').classList.toggle('hidden', r.value !== 'time');
    $('#folderOpts').classList.toggle('hidden', r.value !== 'folder');
  });

// 保留文件夹（用目录选择器选择，多选）
let keepFolders = [];
function renderKeepFolders() {
  $('#keepFolderList').innerHTML = keepFolders.map((p, i) =>
    `<li><span>${p}</span><button class="del" data-i="${i}">×</button></li>`).join('');
  document.querySelectorAll('#keepFolderList .del').forEach(b =>
    b.onclick = () => { keepFolders.splice(+b.dataset.i, 1); renderKeepFolders(); });
}
$('#pickKeepBtn').onclick = () => openDirPicker(path => {
  if (!keepFolders.includes(path)) keepFolders.push(path);
  renderKeepFolders();
});

function strategy() {
  const mode = document.querySelector('input[name=mode]:checked').value;
  return {
    mode,
    keep_n: mode === 'time' ? parseInt($('#keepN').value) || 1 : 0,
    keep_earliest: mode === 'time' ? $('#keepWhich').value === 'earliest' : false,
    keep_folders: keepFolders
  };
}
// 预览：浏览器可打开的文件类型
const IMG_EXT = {'.png':1,'.jpg':1,'.jpeg':1,'.gif':1,'.webp':1,'.svg':1,'.bmp':1,'.ico':1};
const PDF_EXT = {'.pdf':1};
const VIDEO_EXT = {'.mp4':1,'.webm':1,'.ogv':1};
const AUDIO_EXT = {'.mp3':1,'.wav':1,'.ogg':1,'.flac':1};
const TEXT_EXT = {'.txt':1,'.log':1,'.ini':1,'.conf':1,'.cfg':1,'.sh':1,'.bat':1,'.py':1,'.go':1,'.java':1,'.c':1,'.cpp':1,'.h':1,'.js':1,'.ts':1,'.tsx':1,'.jsx':1,'.css':1,'.md':1,'.json':1,'.xml':1,'.yaml':1,'.yml':1,'.csv':1,'.html':1,'.htm':1,'.sql':1};

function previewMeta(path) {
  const dot = path.lastIndexOf('.');
  const ext = dot >= 0 ? path.slice(dot).toLowerCase() : '';
  if (IMG_EXT[ext]) return {ok:true, kind:'img'};
  if (PDF_EXT[ext]) return {ok:true, kind:'pdf'};
  if (VIDEO_EXT[ext]) return {ok:true, kind:'video'};
  if (AUDIO_EXT[ext]) return {ok:true, kind:'audio'};
  if (TEXT_EXT[ext]) return {ok:true, kind:'text'};
  return {ok:false, kind:null};
}
function previewBtn(path) {
  return previewMeta(path).ok
    ? `<button class="ghost preview-file" data-path="${encodeURIComponent(path)}">预览</button>` : '';
}
function previewUrl(path, batch) {
  return '/api/preview?path=' + encodeURIComponent(path) + (batch ? '&batch=' + encodeURIComponent(batch) : '');
}

async function openPreview(path, batch) {
  const meta = previewMeta(path);
  if (!meta.ok) { alert('该文件类型不支持预览'); return; }
  $('#previewTitle').textContent = path;
  const url = previewUrl(path, batch);
  const body = $('#previewBody');
  if (meta.kind === 'img') {
    body.innerHTML = `<img class="pv-img" src="${url}" alt="">`;
  } else if (meta.kind === 'pdf') {
    body.innerHTML = `<iframe class="pv-full" src="${url}"></iframe>`;
  } else if (meta.kind === 'video') {
    body.innerHTML = `<video class="pv-full" controls src="${url}"></video>`;
  } else if (meta.kind === 'audio') {
    body.innerHTML = `<audio class="pv-audio" controls src="${url}"></audio>`;
  } else {
    body.innerHTML = '<pre class="pv-text">加载中…</pre>';
    try {
      const r = await fetch(url);
      if (!r.ok) throw new Error();
      const text = await r.text();
      body.innerHTML = `<pre class="pv-text">${text.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')}</pre>`;
    } catch {
      body.innerHTML = '<pre class="pv-text">加载失败</pre>';
    }
  }
  $('#previewModal').classList.remove('hidden');
}
$('#previewClose').onclick = () => { $('#previewModal').classList.add('hidden'); $('#previewBody').innerHTML = ''; };

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
let currentBatch = '';
let currentBatchFiles = [];

async function loadTrash() {
  const t = await api('/api/trash');
  $('#trashInfo').textContent = `${t.count} 个文件 · ${t.dir}`;
  const batches = (await api('/api/trash/batches')) || [];
  $('#trashBatches').innerHTML = batches.length
    ? batches.map(b => `
      <div class="batch" data-batch="${b.batch_id}">
        <div class="batch-head">
          <strong>批次 ${b.batch_id}</strong>
          <span class="batch-time">删除时间：${fmtTime(b.deleted_at)}</span>
        </div>
        <div class="batch-sub">${b.file_count} 个文件 · ${fmt(b.total_size)}</div>
        <span class="batch-enter">进入查看 ›</span>
      </div>`).join('')
    : '<div class="empty">回收站为空</div>';
  document.querySelectorAll('#trashBatches .batch').forEach(b =>
    b.onclick = () => openBatch(b.dataset.batch));
}

async function openBatch(batch) {
  currentBatch = batch;
  currentBatchFiles = (await api('/api/trash/batches/' + encodeURIComponent(batch))) || [];
  const dt = currentBatchFiles.length ? currentBatchFiles[0].deleted_at : null;
  $('#trashBatchTitle').textContent = batch + (dt ? ' · 删除时间 ' + fmtTime(dt) : '');
  // 按文件夹分组
  const map = {};
  currentBatchFiles.forEach((f, i) => {
    const dir = f.path.includes('/') ? f.path.slice(0, f.path.lastIndexOf('/')) : '/';
    (map[dir] = map[dir] || []).push(i);
  });
  $('#trashBatchList').innerHTML = Object.entries(map).map(([dir, idxs]) => `
    <div class="folder-group">
      <div class="folder-head">
        <span class="path">${dir}</span>
        <button class="primary restore-folder" data-idx="${idxs.join(',')}">恢复此文件夹</button>
      </div>
      ${idxs.map(i => {
        const f = currentBatchFiles[i];
        return `<div class="file-row">
          <span class="path">${f.path}</span>
          ${previewBtn(f.path)}
          <span class="time">${fmt(f.size)}</span>
          <button class="ghost restore-file" data-idx="${i}">恢复</button>
        </div>`;
      }).join('')}
    </div>`).join('');
  $('#trashSelectInfo').textContent = `共 ${currentBatchFiles.length} 个文件`;
  $('#trashModal').classList.remove('hidden');
  renderTrashActions();
}

function renderTrashActions() {
  document.querySelectorAll('#trashBatchList .restore-file').forEach(b =>
    b.onclick = () => restoreFiles([+b.dataset.idx]));
  document.querySelectorAll('#trashBatchList .restore-folder').forEach(b =>
    b.onclick = () => restoreFiles(b.dataset.idx.split(',').map(Number)));
  document.querySelectorAll('#trashBatchList .preview-file').forEach(b =>
    b.onclick = () => openPreview(decodeURIComponent(b.dataset.path), currentBatch));
}

async function restoreFiles(idxs) {
  if (!idxs.length) return;
  const paths = idxs.map(i => currentBatchFiles[i].path);
  if (!confirm(`确定恢复 ${paths.length} 个文件到原位置？`)) return;
  const res = await api('/api/trash/restore', 'POST', { batch: currentBatch, paths });
  alert(`已恢复 ${res.count} 个文件`);
  currentBatchFiles = currentBatchFiles.filter((_, i) => !idxs.includes(i));
  if (currentBatchFiles.length === 0) {
    $('#trashModal').classList.add('hidden');
  } else {
    openBatch(currentBatch);
  }
  loadTrash(); loadDups();
}
$('#trashRestoreAll').onclick = () =>
  restoreFiles(currentBatchFiles.map((_, i) => i));
$('#trashModalClose').onclick = () => $('#trashModal').classList.add('hidden');
$('#emptyTrashBtn').onclick = async () => {
  if (!confirm('确认清空回收站？此操作不可恢复！')) return;
  await api('/api/trash/empty', 'POST');
  loadTrash();
};

loadFolders(); initBrowser(); loadDups(); loadTrash();
api('/api/version').then(v => { if (v && v.version) $('#appVersion').textContent = v.version; }).catch(() => {});