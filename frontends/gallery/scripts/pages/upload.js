// Upload flow: file select/drop → SHA-256 → sign auth event → PUT /upload

var uploadState = {
    file: null,
    hash: null
};

function openUploadModal() {
    if (!window.sessionManager || !window.sessionManager.isAuthenticated()) {
        if (typeof showMessage === 'function') showMessage('Please log in to upload files', 'error');
        return;
    }
    resetUploadModal();
    document.getElementById('upload-modal').classList.remove('hidden');
}

function closeUploadModal() {
    document.getElementById('upload-modal').classList.add('hidden');
    resetUploadModal();
}

function handleUploadOverlayClick(event) {
    if (event.target === document.getElementById('upload-modal')) {
        closeUploadModal();
    }
}

function resetUploadModal() {
    uploadState.file = null;
    uploadState.hash = null;

    const fileInput = document.getElementById('upload-file-input');
    if (fileInput) fileInput.value = '';

    setEl('upload-file-info', { hidden: true });
    setEl('upload-progress', { hidden: true });
    setEl('upload-result', { hidden: true });

    const submitBtn = document.getElementById('upload-submit-btn');
    if (submitBtn) {
        submitBtn.disabled = true;
        submitBtn.textContent = 'Upload';
    }

    const dropContent = document.getElementById('upload-drop-content');
    if (dropContent) {
        dropContent.innerHTML = '<div class="text-4xl mb-2">📁</div><p class="text-white/60 text-sm">Drag &amp; drop a file or click to browse</p>';
    }
}

function handleUploadDrop(event) {
    event.preventDefault();
    const zone = document.getElementById('upload-drop-zone');
    zone.classList.remove('border-purple-400', 'bg-purple-500/10');
    const file = event.dataTransfer.files[0];
    if (file) selectUploadFile(file);
}

function handleUploadFileSelect(event) {
    const file = event.target.files[0];
    if (file) selectUploadFile(file);
}

async function selectUploadFile(file) {
    uploadState.file = file;
    uploadState.hash = null;

    document.getElementById('upload-file-name').textContent = file.name;
    document.getElementById('upload-file-meta').textContent =
        `${formatUploadBytes(file.size)} • ${file.type || 'unknown type'}`;
    setEl('upload-file-info', { hidden: false });

    const dropContent = document.getElementById('upload-drop-content');
    if (dropContent) {
        dropContent.innerHTML = '<div class="text-4xl mb-2">✅</div><p class="text-white/60 text-sm">File selected — click to change</p>';
    }

    const submitBtn = document.getElementById('upload-submit-btn');
    submitBtn.disabled = true;
    submitBtn.textContent = 'Computing hash...';

    try {
        const buffer = await file.arrayBuffer();
        const hashBuffer = await crypto.subtle.digest('SHA-256', buffer);
        uploadState.hash = Array.from(new Uint8Array(hashBuffer))
            .map(b => b.toString(16).padStart(2, '0'))
            .join('');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Upload';
    } catch (err) {
        submitBtn.textContent = 'Upload';
        if (typeof showMessage === 'function') showMessage('Failed to compute file hash', 'error');
    }
}

async function submitUpload() {
    if (!uploadState.file || !uploadState.hash) return;
    if (!window.blossomSigner) {
        if (typeof showMessage === 'function') showMessage('Signer not available — please refresh', 'error');
        return;
    }

    const submitBtn = document.getElementById('upload-submit-btn');
    const statusText = document.getElementById('upload-status-text');
    const progressBar = document.getElementById('upload-progress-bar');
    const resultDiv = document.getElementById('upload-result');

    submitBtn.disabled = true;
    setEl('upload-progress', { hidden: false });
    setEl('upload-result', { hidden: true });

    try {
        statusText.textContent = 'Signing authorization...';
        progressBar.style.width = '15%';

        const authHeader = await window.blossomSigner.signAuthEvent('upload', uploadState.hash);

        statusText.textContent = 'Uploading...';
        progressBar.style.width = '40%';

        const response = await fetch('/upload', {
            method: 'PUT',
            headers: {
                'Authorization': authHeader,
                'Content-Type': uploadState.file.type || 'application/octet-stream',
                'X-SHA-256': uploadState.hash,
                'X-Content-Length': uploadState.file.size.toString()
            },
            body: uploadState.file
        });

        progressBar.style.width = '90%';

        if (!response.ok) {
            const err = await response.json().catch(() => ({ message: `HTTP ${response.status}` }));
            throw new Error(err.message || `Upload failed (${response.status})`);
        }

        const blob = await response.json();
        progressBar.style.width = '100%';
        statusText.textContent = 'Upload complete!';

        const safeUrl = escapeHtml(blob.url);
        setEl('upload-result', { hidden: false });
        resultDiv.innerHTML = `
            <div class="bg-green-900/40 border border-green-500/50 rounded-lg p-3 text-sm">
                <p class="font-medium text-green-300 mb-2">Upload successful!</p>
                <div class="flex items-center gap-2">
                    <input type="text" value="${safeUrl}" readonly
                           class="flex-1 bg-white/5 border border-white/10 rounded px-2 py-1 text-xs text-white/70 truncate">
                    <button onclick="copyUploadUrl('${safeUrl}')"
                            class="shrink-0 text-xs bg-purple-500 hover:bg-purple-600 px-3 py-1 rounded transition">
                        Copy
                    </button>
                </div>
            </div>
        `;

        submitBtn.textContent = 'Upload another';
        submitBtn.disabled = false;
        submitBtn.onclick = () => { resetUploadModal(); };

        // Reload gallery if present
        if (typeof initGallery === 'function') {
            setTimeout(initGallery, 300);
        }

    } catch (err) {
        setEl('upload-progress', { hidden: true });
        submitBtn.disabled = false;
        submitBtn.textContent = 'Retry';
        setEl('upload-result', { hidden: false });
        resultDiv.innerHTML = `
            <div class="bg-red-900/40 border border-red-500/50 rounded-lg p-3 text-sm text-red-300">
                ${escapeHtml(err.message)}
            </div>
        `;
    }
}

function copyUploadUrl(url) {
    navigator.clipboard.writeText(url).then(() => {
        if (typeof showMessage === 'function') showMessage('URL copied!', 'success');
    }).catch(() => {
        if (typeof showMessage === 'function') showMessage('Failed to copy URL', 'error');
    });
}

function formatUploadBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
}

function escapeHtml(str) {
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

function setEl(id, opts) {
    const el = document.getElementById(id);
    if (!el) return;
    if (opts.hidden !== undefined) {
        if (opts.hidden) el.classList.add('hidden');
        else el.classList.remove('hidden');
    }
}

console.log('📤 Upload script loaded');
