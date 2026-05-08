/**
 * Settings Page
 * Manages relay configuration
 */

// Initialize page
function initializePage() {
  console.log('⚙️ Settings page loading...');

  // Check if relay manager is available
  if (!window.relayManager) {
    console.warn('Relay manager not initialized yet');
  }

  // Load settings
  updateSettingsUI();
}

// Update all settings UI
function updateSettingsUI() {
  if (!window.relayManager) return;

  const info = window.relayManager.getRelayInfo();

  // Index relays
  updateIndexRelaysUI(info.index);

  // User relays
  updateUserRelaysUI(info.user);

  // App relays
  updateAppRelaysUI(info.app);

  // Connection status
  updateConnectionStatusUI(info.connected);
}

// Update index relays UI
function updateIndexRelaysUI(indexRelays) {
  const list = document.getElementById('index-relays-list');
  if (!list) return;

  if (indexRelays.length === 0) {
    list.innerHTML = '<div class="text-sm text-gray-500 bg-gray-800/50 px-4 py-3 rounded">No index relays configured</div>';
    return;
  }

  list.innerHTML = indexRelays.map((url, i) => `
    <div class="flex items-center justify-between bg-gray-800/50 px-4 py-3 rounded">
      <span class="text-sm text-gray-300 break-all">${url}</span>
      <button onclick="removeIndexRelay(${i})" class="ml-4 text-red-400 hover:text-red-300 text-sm whitespace-nowrap">
        Remove
      </button>
    </div>
  `).join('');
}

// Update user relays UI
function updateUserRelaysUI(userRelays) {
  const readList = document.getElementById('user-read-list');
  const writeList = document.getElementById('user-write-list');
  const bothList = document.getElementById('user-both-list');

  if (readList) {
    readList.innerHTML = userRelays.read.length > 0
      ? userRelays.read.map(url => `<div class="bg-gray-800/30 px-3 py-2 rounded text-gray-300 break-all">${url}</div>`).join('')
      : '<div class="text-gray-500">None</div>';
  }

  if (writeList) {
    writeList.innerHTML = userRelays.write.length > 0
      ? userRelays.write.map(url => `<div class="bg-gray-800/30 px-3 py-2 rounded text-gray-300 break-all">${url}</div>`).join('')
      : '<div class="text-gray-500">None</div>';
  }

  if (bothList) {
    bothList.innerHTML = userRelays.both.length > 0
      ? userRelays.both.map(url => `<div class="bg-gray-800/30 px-3 py-2 rounded text-gray-300 break-all">${url}</div>`).join('')
      : '<div class="text-gray-500">None</div>';
  }
}

// Update app relays UI
function updateAppRelaysUI(appRelays) {
  const list = document.getElementById('app-relays-list');
  if (!list) return;

  if (appRelays.length === 0) {
    list.innerHTML = '<div class="text-sm text-gray-500 bg-gray-800/50 px-4 py-3 rounded">No app relays configured</div>';
    return;
  }

  list.innerHTML = appRelays.map((url, i) => `
    <div class="flex items-center justify-between bg-gray-800/50 px-4 py-3 rounded">
      <span class="text-sm text-gray-300 break-all">${url}</span>
      <button onclick="removeAppRelay(${i})" class="ml-4 text-red-400 hover:text-red-300 text-sm whitespace-nowrap">
        Remove
      </button>
    </div>
  `).join('');
}

// Update connection status UI
function updateConnectionStatusUI(connected) {
  const statusDiv = document.getElementById('connection-status');
  if (!statusDiv) return;

  const info = window.relayManager.getRelayInfo();
  const totalCount = info.app.length + info.index.length;
  const connectedCount = connected.length;

  statusDiv.innerHTML = `
    <div class="mb-3">
      <span class="font-medium text-white text-lg">${connectedCount}</span>
      <span class="text-gray-400"> / ${totalCount} relays connected</span>
    </div>
    ${connected.length > 0 ? `
      <div class="space-y-1">
        ${connected.map(url => `
          <div class="text-xs text-green-400 bg-green-900/20 px-3 py-2 rounded">✅ ${url}</div>
        `).join('')}
      </div>
    ` : '<div class="text-gray-500">No active connections</div>'}
  `;
}

// Add index relay
function addIndexRelay() {
  const input = document.getElementById('new-index-relay');
  const url = input.value.trim();

  if (!url) {
    showMessage('⚠️ Please enter a relay URL', 'warning');
    return;
  }

  if (!url.startsWith('wss://') && !url.startsWith('ws://')) {
    showMessage('❌ Relay URL must start with wss:// or ws://', 'error');
    return;
  }

  const info = window.relayManager.getRelayInfo();
  if (info.index.includes(url)) {
    showMessage('⚠️ Relay already in list', 'warning');
    return;
  }

  window.relayManager.setIndexRelays([...info.index, url]);
  input.value = '';
  updateSettingsUI();
  showMessage('✅ Index relay added', 'success');
}

// Remove index relay
function removeIndexRelay(index) {
  const info = window.relayManager.getRelayInfo();
  const newRelays = info.index.filter((_, i) => i !== index);
  window.relayManager.setIndexRelays(newRelays);
  updateSettingsUI();
  showMessage('✅ Index relay removed', 'success');
}

// Add app relay
async function addAppRelay() {
  const input = document.getElementById('new-app-relay');
  const url = input.value.trim();

  if (!url) {
    showMessage('⚠️ Please enter a relay URL', 'warning');
    return;
  }

  if (!url.startsWith('wss://') && !url.startsWith('ws://')) {
    showMessage('❌ Relay URL must start with wss:// or ws://', 'error');
    return;
  }

  const info = window.relayManager.getRelayInfo();
  if (info.app.includes(url)) {
    showMessage('⚠️ Relay already in list', 'warning');
    return;
  }

  await window.relayManager.setAppRelays([...info.app, url]);
  input.value = '';
  updateSettingsUI();
  showMessage('✅ App relay added', 'success');
}

// Remove app relay
async function removeAppRelay(index) {
  const info = window.relayManager.getRelayInfo();
  const newRelays = info.app.filter((_, i) => i !== index);
  await window.relayManager.setAppRelays(newRelays);
  updateSettingsUI();
  showMessage('✅ App relay removed', 'success');
}

// Reset app relays to mailboxes
async function resetAppRelays() {
  const userRelayList = window.relayManager.getUserRelayList();
  await window.relayManager.setAppRelays(userRelayList);
  updateSettingsUI();
  showMessage('✅ App relays reset to mailboxes', 'success');
}

// Initialize settings page (called by htmx after content swap or on initial load)
function initSettingsPage() {
  console.log('⚙️ Settings: Initializing page');

  // Check if session manager is ready
  if (!window.sessionManager) {
    console.log('⚙️ Settings: Session manager not ready, waiting...');
    setTimeout(initSettingsPage, 100);
    return;
  }

  // Check status
  const status = window.sessionManager.getStatus();
  if (status === 'initializing') {
    console.log('⚙️ Settings: Session initializing, waiting...');
    setTimeout(initSettingsPage, 100);
    return;
  }

  // Check if user is authenticated
  if (!window.sessionManager.isAuthenticated()) {
    console.log('⚙️ Settings: Not authenticated, showing message');
    showMessage('⚠️ Please log in to view settings', 'warning');
    setTimeout(() => {
      // Use htmx navigation
      if (window.htmx) {
        htmx.ajax('GET', '/', {target: '#main-content', swap: 'innerHTML'});
        history.pushState({}, '', '/');
      } else {
        window.location.href = '/';
      }
    }, 2000);
    return;
  }

  // Check if relay manager exists
  if (!window.relayManager) {
    console.log('⚙️ Settings: Relay manager not available yet, retrying...');
    setTimeout(initSettingsPage, 100);
    return;
  }

  // Check if relay manager has any data (index relays are always present)
  const info = window.relayManager.getRelayInfo();
  if (info && info.index && info.index.length > 0) {
    console.log('⚙️ Settings: Relay manager ready, displaying now');
    initializePage();
  } else {
    // Relay manager not initialized yet, wait for it
    console.log('⚙️ Settings: Waiting for relay manager to initialize...');

    // Listen for initialized event
    const onInitialized = () => {
      console.log('⚙️ Settings: Relay manager initialized, loading page');
      initializePage();
    };

    window.relayManager.on('initialized', onInitialized);

    // Also set a timeout fallback in case the event was missed
    setTimeout(() => {
      const currentInfo = window.relayManager.getRelayInfo();
      if (currentInfo) {
        console.log('⚙️ Settings: Timeout fallback, loading page');
        initializePage();
      }
    }, 2000);
  }
}

console.log('⚙️ Settings script loaded');
