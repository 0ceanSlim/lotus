/**
 * User Media Page
 * Displays user's uploaded media and storage statistics
 */

// Format bytes to human-readable string
function formatBytes(bytes) {
  if (bytes === 0) return '0 Bytes';

  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));

  return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
}

// Format timestamp to readable date
function formatDate(timestamp) {
  const date = new Date(timestamp * 1000);
  return date.toLocaleDateString() + ' ' + date.toLocaleTimeString();
}

// Get file extension or category from MIME type
function getFileCategory(mimeType) {
  if (mimeType.startsWith('image/')) return '🖼️ Image';
  if (mimeType.startsWith('video/')) return '🎥 Video';
  if (mimeType.startsWith('audio/')) return '🎵 Audio';
  if (mimeType.startsWith('text/')) return '📝 Text';
  if (mimeType.includes('pdf')) return '📄 PDF';
  if (mimeType.includes('zip') || mimeType.includes('compressed')) return '📦 Archive';
  return '📎 File';
}

// Helper function to redirect home using htmx
function redirectToHome() {
  if (window.htmx) {
    htmx.ajax('GET', '/', {target: '#main-content', swap: 'innerHTML'});
    history.pushState({}, '', '/');
  } else {
    window.location.href = '/';
  }
}

// Load and display user statistics
async function loadStats() {
  try {
    const response = await fetch('/api/user/stats');

    if (!response.ok) {
      if (response.status === 401) {
        showMessage('⚠️ Please log in to view your media', 'warning');
        setTimeout(redirectToHome, 2000);
        return;
      }
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();

    if (!data.success) {
      throw new Error(data.error || 'Failed to load stats');
    }

    const stats = data.stats;

    // Update storage used
    document.getElementById('storage-used').textContent = formatBytes(stats.total_storage);
    document.getElementById('storage-quota').textContent = `of ${formatBytes(stats.quota_bytes)}`;

    // Update file count
    document.getElementById('file-count').textContent = stats.file_count.toLocaleString();

    // Update percentage
    const percent = Math.round(stats.percent_used * 10) / 10;
    document.getElementById('storage-percent').textContent = `${percent}%`;
    document.getElementById('storage-bar').style.width = `${Math.min(percent, 100)}%`;

    // Change bar color if over 80%
    const bar = document.getElementById('storage-bar');
    if (percent > 80) {
      bar.classList.remove('from-purple-500', 'to-pink-500');
      bar.classList.add('from-red-500', 'to-orange-500');
    }

    // Find most common type
    if (stats.file_types && stats.file_types.length > 0) {
      const sortedTypes = stats.file_types.sort((a, b) => b.count - a.count);
      const mostCommon = sortedTypes[0];
      document.getElementById('common-type').textContent = getFileCategory(mostCommon.mime_type);
      document.getElementById('common-count').textContent = `${mostCommon.count} files`;

      // Display file types breakdown
      const fileTypesList = document.getElementById('file-types-list');
      fileTypesList.innerHTML = sortedTypes.map(type => `
        <div class="bg-gray-800/50 rounded-lg p-3">
          <div class="flex items-center justify-between mb-1">
            <span class="font-medium">${getFileCategory(type.mime_type)}</span>
            <span class="text-sm text-gray-400">${type.count}</span>
          </div>
          <div class="text-xs text-gray-400">${formatBytes(type.total_size)}</div>
        </div>
      `).join('');

      document.getElementById('file-types-container').classList.remove('hidden');
    }

    // Show stats container
    document.getElementById('stats-loading').classList.add('hidden');
    document.getElementById('stats-container').classList.remove('hidden');

  } catch (error) {
    console.error('Failed to load stats:', error);
    document.getElementById('stats-loading').innerHTML = `
      <div class="text-center text-red-400">
        <p>❌ Failed to load statistics</p>
        <p class="text-sm mt-2">${error.message}</p>
      </div>
    `;
  }
}

// Load and display user media
async function loadMedia() {
  try {
    const response = await fetch('/api/user/media');

    if (!response.ok) {
      if (response.status === 401) {
        return; // Already handled in loadStats
      }
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();

    if (!data.success) {
      throw new Error(data.error || 'Failed to load media');
    }

    const media = data.media || [];

    document.getElementById('media-loading').classList.add('hidden');

    if (media.length === 0) {
      document.getElementById('no-media').classList.remove('hidden');
      return;
    }

    // Display media grid
    const mediaGrid = document.getElementById('media-grid');
    mediaGrid.innerHTML = media.map(item => createMediaCard(item)).join('');
    mediaGrid.classList.remove('hidden');

  } catch (error) {
    console.error('Failed to load media:', error);
    document.getElementById('media-loading').innerHTML = `
      <div class="text-center text-red-400">
        <p>❌ Failed to load media</p>
        <p class="text-sm mt-2">${error.message}</p>
      </div>
    `;
  }
}

// Create media card HTML
function createMediaCard(item) {
  const isImage = item.type.startsWith('image/');
  const isVideo = item.type.startsWith('video/');
  const category = getFileCategory(item.type);

  return `
    <div class="bg-white/10 backdrop-blur-md rounded-lg overflow-hidden hover:bg-white/20 transition-all group">
      <div class="aspect-square bg-gray-800/50 flex items-center justify-center relative overflow-hidden">
        ${isImage ? `
          <img src="${item.url}"
               alt="Media"
               class="w-full h-full object-cover"
               loading="lazy"
               onerror="this.style.display='none'; this.nextElementSibling.style.display='flex'">
          <div class="hidden w-full h-full items-center justify-center text-4xl">
            ${category.split(' ')[0]}
          </div>
        ` : `
          <div class="text-4xl">${category.split(' ')[0]}</div>
        `}
        <div class="absolute inset-0 bg-black/60 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center gap-2">
          <a href="${item.url}" target="_blank"
             class="bg-white/20 hover:bg-white/30 p-2 rounded-lg backdrop-blur-sm"
             title="Open">
            👁️
          </a>
          <button onclick="copyUrl('${item.url}')"
                  class="bg-white/20 hover:bg-white/30 p-2 rounded-lg backdrop-blur-sm"
                  title="Copy URL">
            📋
          </button>
          <button onclick="deleteMedia('${item.sha256}')"
                  class="bg-red-500/70 hover:bg-red-600/70 p-2 rounded-lg backdrop-blur-sm"
                  title="Delete">
            🗑️
          </button>
        </div>
      </div>
      <div class="p-3">
        <div class="text-xs text-gray-400 mb-1">${category}</div>
        <div class="text-sm font-medium mb-1 truncate">${item.sha256.substring(0, 16)}...</div>
        <div class="flex justify-between text-xs text-gray-400">
          <span>${formatBytes(item.size)}</span>
          <span>${formatDate(item.uploaded)}</span>
        </div>
      </div>
    </div>
  `;
}

// Copy URL to clipboard
function copyUrl(url) {
  navigator.clipboard.writeText(url).then(() => {
    showMessage('✅ URL copied to clipboard!', 'success');
  }).catch(err => {
    console.error('Failed to copy:', err);
    showMessage('❌ Failed to copy URL', 'error');
  });
}

// Delete media
async function deleteMedia(hash) {
  if (!confirm('Are you sure you want to delete this file?')) {
    return;
  }

  try {
    // Check if blossom signer is available
    if (!window.blossomSigner) {
      throw new Error('Blossom signer not initialized');
    }

    // Use the blossom signer to create an authenticated delete request
    const response = await window.blossomSigner.deleteBlob(hash);

    if (!response.ok) {
      const errorText = await response.text().catch(() => 'Unknown error');
      throw new Error(`HTTP ${response.status}: ${errorText}`);
    }

    showMessage('✅ File deleted successfully', 'success');

    // Reload the page data
    setTimeout(() => {
      loadStats();
      loadMedia();
    }, 1000);

  } catch (error) {
    console.error('Failed to delete:', error);
    showMessage('❌ Failed to delete file: ' + error.message, 'error');
  }
}

// Initialize page - called by central SPA router
function initMyMediaPage() {
  console.log('📊 User Media: Initializing');

  // Check if session manager is ready
  if (!window.sessionManager) {
    console.log('📊 User Media: Session manager not ready, waiting...');
    setTimeout(initMyMediaPage, 100);
    return;
  }

  // Check status
  const status = window.sessionManager.getStatus();
  if (status === 'initializing') {
    console.log('📊 User Media: Session initializing, waiting...');
    setTimeout(initMyMediaPage, 100);
    return;
  }

  // Check if user is authenticated
  if (!window.sessionManager.isAuthenticated()) {
    showMessage('⚠️ Please log in to view your media', 'warning');
    setTimeout(redirectToHome, 2000);
    return;
  }

  // Load data
  loadStats();
  loadMedia();
}

console.log('📊 User Media script loaded');
