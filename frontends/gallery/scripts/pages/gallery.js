// Gallery page script with infinite scroll and profile fetching
var galleryState = {
    offset: 0,
    itemsPerPage: 12,
    totalItems: 0,
    isLoading: false,
    hasMore: true,
    profileCache: new Map(),
    profileFetchQueue: new Set()
};

// Index relay for profile fetching
var INDEX_RELAY = 'wss://purplepag.es/';
var indexRelayWs = null;
var profileSubscriptions = new Map();

async function fetchServerStats() {
    try {
        const response = await fetch('/stats');
        if (!response.ok) {
            throw new Error('Failed to fetch server stats');
        }

        const stats = await response.json();

        document.getElementById('total-storage').textContent = formatBytes(stats.bytes_stored);
        document.getElementById('total-blobs').textContent = stats.blob_count.toLocaleString();
        document.getElementById('total-users').textContent = stats.pubkey_count.toLocaleString();
    } catch (error) {
        console.error('Error fetching server stats:', error);
        document.getElementById('total-storage').textContent = 'Error';
        document.getElementById('total-blobs').textContent = 'Error';
        document.getElementById('total-users').textContent = 'Error';
    }
}

async function fetchMedia() {
    if (galleryState.isLoading || !galleryState.hasMore) return;

    galleryState.isLoading = true;
    showLoading(true);

    try {
        const response = await fetch(`/list-all?limit=${galleryState.itemsPerPage}&offset=${galleryState.offset}`);

        if (!response.ok) {
            throw new Error('Failed to fetch media');
        }

        const data = await response.json();
        galleryState.totalItems = data.total;

        if (data.blobs && data.blobs.length > 0) {
            appendMedia(data.blobs);
            galleryState.offset += data.blobs.length;

            // Check if we've loaded everything
            if (galleryState.offset >= galleryState.totalItems) {
                galleryState.hasMore = false;
                document.getElementById('end-message').classList.remove('hidden');
            }
        } else {
            galleryState.hasMore = false;
            if (galleryState.offset === 0) {
                document.getElementById('gallery').innerHTML =
                    '<div class="col-span-full text-center text-white/80 py-12">No media found</div>';
            }
        }

        updateCurrentInfo();
        document.getElementById('error-container').innerHTML = '';
    } catch (error) {
        console.error('Error fetching media:', error);
        document.getElementById('error-container').innerHTML =
            `<div class="bg-red-500/20 border border-red-500 rounded-lg p-4 text-center text-white mb-6">Error loading media: ${error.message}</div>`;
    } finally {
        galleryState.isLoading = false;
        showLoading(false);
    }
}

function appendMedia(blobs) {
    const gallery = document.getElementById('gallery');

    blobs.forEach(blob => {
        const card = createCard(blob);
        gallery.appendChild(card);

        // Queue profile fetch if pubkey exists
        if (blob.pubkey && !galleryState.profileCache.has(blob.pubkey)) {
            galleryState.profileFetchQueue.add(blob.pubkey);
        }
    });

    // Batch fetch profiles
    if (galleryState.profileFetchQueue.size > 0) {
        fetchProfiles(Array.from(galleryState.profileFetchQueue));
        galleryState.profileFetchQueue.clear();
    }
}

function createCard(blob) {
    const card = document.createElement('div');
    card.className = 'bg-white/10 backdrop-blur-lg rounded-xl overflow-hidden border border-white/20 hover:scale-105 hover:shadow-2xl transition';
    card.dataset.pubkey = blob.pubkey || '';

    const mediaElement = createMediaElement(blob);
    const body = document.createElement('div');
    body.className = 'p-4';

    // Media info
    const title = document.createElement('div');
    title.className = 'text-sm font-semibold text-white/90 mb-2 truncate';
    title.textContent = blob.sha256.substring(0, 16) + '...';

    const meta = document.createElement('div');
    meta.className = 'flex justify-between text-xs text-white/70 mb-3';

    const size = document.createElement('span');
    size.textContent = formatBytes(blob.size);

    const date = document.createElement('span');
    date.textContent = new Date(blob.uploaded * 1000).toLocaleDateString();

    meta.appendChild(size);
    meta.appendChild(date);

    const type = document.createElement('span');
    type.className = 'inline-block bg-purple-500/50 px-2 py-1 rounded text-xs text-white mb-3';
    type.textContent = blob.type.split('/')[0];

    // Profile info section
    const profileSection = document.createElement('div');
    profileSection.className = 'border-t border-white/10 pt-3 mt-2';
    profileSection.id = `profile-${blob.sha256}`;

    if (blob.pubkey) {
        const npub = hexToNpub(blob.pubkey);
        profileSection.innerHTML = `
            <div class="flex items-center gap-2">
                <div class="w-8 h-8 rounded-full bg-purple-500/30 flex items-center justify-center text-xs">
                    ?
                </div>
                <div class="flex-1 min-w-0">
                    <div class="text-xs text-white/90 truncate" id="name-${blob.sha256}">
                        ${npub.substring(0, 12)}...
                    </div>
                    <div class="text-xs text-white/50 truncate">
                        ${npub.substring(0, 20)}...
                    </div>
                </div>
            </div>
        `;
    } else {
        profileSection.innerHTML = `
            <div class="text-xs text-white/50 italic">Anonymous upload</div>
        `;
    }

    body.appendChild(title);
    body.appendChild(meta);
    body.appendChild(type);
    body.appendChild(profileSection);

    card.appendChild(mediaElement);
    card.appendChild(body);

    // Add click handler for images
    if (blob.type.startsWith('image/')) {
        mediaElement.style.cursor = 'pointer';
        mediaElement.onclick = () => openModal(blob.url);
    }

    return card;
}

function createMediaElement(blob) {
    const container = document.createElement('div');
    container.className = 'w-full h-64 bg-white/5 flex items-center justify-center';

    if (blob.type.startsWith('image/')) {
        const img = document.createElement('img');
        img.src = blob.url;
        img.alt = 'Media';
        img.loading = 'lazy';
        img.className = 'w-full h-full object-cover';
        container.appendChild(img);
    } else if (blob.type.startsWith('video/')) {
        const video = document.createElement('video');
        video.src = blob.url;
        video.controls = true;
        video.preload = 'metadata';
        video.className = 'w-full h-full';
        container.appendChild(video);
    } else if (blob.type.startsWith('audio/')) {
        const audio = document.createElement('audio');
        audio.src = blob.url;
        audio.controls = true;
        audio.preload = 'metadata';
        audio.className = 'w-full px-4';
        container.appendChild(audio);
    } else {
        const icon = document.createElement('div');
        icon.className = 'text-6xl';
        icon.textContent = getFileIcon(blob.type);
        container.appendChild(icon);
    }

    return container;
}

function getFileIcon(mimeType) {
    if (mimeType.includes('pdf')) return '📄';
    if (mimeType.includes('zip') || mimeType.includes('compressed')) return '📦';
    if (mimeType.includes('text')) return '📝';
    return '📎';
}

// Profile fetching via WebSocket to index relay
function fetchProfiles(pubkeys) {
    if (!pubkeys || pubkeys.length === 0) return;

    // Filter out already cached
    const toFetch = pubkeys.filter(pk => !galleryState.profileCache.has(pk));
    if (toFetch.length === 0) return;

    console.log(`📡 Fetching ${toFetch.length} profiles from index relay`);

    // Connect to relay if not connected
    if (!indexRelayWs || indexRelayWs.readyState !== WebSocket.OPEN) {
        connectToIndexRelay(() => {
            sendProfileRequest(toFetch);
        });
    } else {
        sendProfileRequest(toFetch);
    }
}

function connectToIndexRelay(callback) {
    if (indexRelayWs && indexRelayWs.readyState === WebSocket.OPEN) {
        callback();
        return;
    }

    console.log('🔌 Connecting to index relay:', INDEX_RELAY);
    indexRelayWs = new WebSocket(INDEX_RELAY);

    indexRelayWs.onopen = () => {
        console.log('✅ Connected to index relay');
        callback();
    };

    indexRelayWs.onmessage = (event) => {
        handleRelayMessage(event.data);
    };

    indexRelayWs.onerror = (error) => {
        console.error('❌ Index relay error:', error);
    };

    indexRelayWs.onclose = () => {
        console.log('🔌 Disconnected from index relay');
        indexRelayWs = null;
    };
}

function sendProfileRequest(pubkeys) {
    const subId = 'profiles_' + Date.now();
    const req = JSON.stringify(['REQ', subId, { kinds: [0], authors: pubkeys }]);

    profileSubscriptions.set(subId, pubkeys);
    indexRelayWs.send(req);

    // Auto-close subscription after timeout
    setTimeout(() => {
        if (indexRelayWs && indexRelayWs.readyState === WebSocket.OPEN) {
            indexRelayWs.send(JSON.stringify(['CLOSE', subId]));
        }
        profileSubscriptions.delete(subId);
    }, 10000);
}

function handleRelayMessage(data) {
    try {
        const message = JSON.parse(data);
        const [type, ...rest] = message;

        if (type === 'EVENT') {
            const [subId, event] = rest;
            if (event.kind === 0) {
                handleProfileEvent(event);
            }
        }
    } catch (error) {
        console.error('Failed to parse relay message:', error);
    }
}

function handleProfileEvent(event) {
    try {
        const profile = JSON.parse(event.content);
        const pubkey = event.pubkey;

        // Cache the profile
        galleryState.profileCache.set(pubkey, profile);

        // Update all cards with this pubkey
        updateProfileDisplay(pubkey, profile);
    } catch (error) {
        console.error('Failed to parse profile:', error);
    }
}

function updateProfileDisplay(pubkey, profile) {
    const cards = document.querySelectorAll(`[data-pubkey="${pubkey}"]`);

    cards.forEach(card => {
        const sha256 = card.querySelector('[id^="profile-"]')?.id.replace('profile-', '');
        if (!sha256) return;

        const profileSection = document.getElementById(`profile-${sha256}`);
        if (!profileSection) return;

        const displayName = profile.display_name || profile.name || hexToNpub(pubkey).substring(0, 12) + '...';
        const picture = profile.picture;
        const npub = hexToNpub(pubkey);

        profileSection.innerHTML = `
            <div class="flex items-center gap-2">
                ${picture
                    ? `<img src="${picture}" class="w-8 h-8 rounded-full object-cover" onerror="this.style.display='none'; this.nextElementSibling.style.display='flex'">
                       <div class="w-8 h-8 rounded-full bg-purple-500/30 items-center justify-center text-xs hidden">?</div>`
                    : `<div class="w-8 h-8 rounded-full bg-purple-500/30 flex items-center justify-center text-xs">
                        ${displayName.charAt(0).toUpperCase()}
                       </div>`
                }
                <div class="flex-1 min-w-0">
                    <div class="text-xs text-white/90 truncate font-medium">
                        ${displayName}
                    </div>
                    <div class="text-xs text-white/50 truncate">
                        ${npub.substring(0, 20)}...
                    </div>
                </div>
            </div>
        `;
    });
}

// Convert hex pubkey to npub
function hexToNpub(hex) {
    if (!hex) return 'unknown';
    try {
        // Simple bech32 encoding for npub
        const ALPHABET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l';
        const hrp = 'npub';

        // Convert hex to 5-bit groups
        const data = [];
        for (let i = 0; i < hex.length; i += 2) {
            data.push(parseInt(hex.substr(i, 2), 16));
        }

        // Convert 8-bit to 5-bit
        let acc = 0;
        let bits = 0;
        const ret = [];
        for (const value of data) {
            acc = (acc << 8) | value;
            bits += 8;
            while (bits >= 5) {
                bits -= 5;
                ret.push((acc >> bits) & 31);
            }
        }
        if (bits > 0) {
            ret.push((acc << (5 - bits)) & 31);
        }

        // Compute checksum
        const polymod = (values) => {
            const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
            let chk = 1;
            for (const v of values) {
                const b = chk >> 25;
                chk = ((chk & 0x1ffffff) << 5) ^ v;
                for (let i = 0; i < 5; i++) {
                    if ((b >> i) & 1) chk ^= GEN[i];
                }
            }
            return chk;
        };

        const hrpExpand = (hrp) => {
            const ret = [];
            for (const c of hrp) {
                ret.push(c.charCodeAt(0) >> 5);
            }
            ret.push(0);
            for (const c of hrp) {
                ret.push(c.charCodeAt(0) & 31);
            }
            return ret;
        };

        const values = hrpExpand(hrp).concat(ret);
        const checksum = polymod(values.concat([0, 0, 0, 0, 0, 0])) ^ 1;
        const checksumChars = [];
        for (let i = 0; i < 6; i++) {
            checksumChars.push((checksum >> (5 * (5 - i))) & 31);
        }

        return hrp + '1' + ret.concat(checksumChars).map(i => ALPHABET[i]).join('');
    } catch (e) {
        return hex.substring(0, 12) + '...';
    }
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
}

function updateCurrentInfo() {
    const loaded = Math.min(galleryState.offset, galleryState.totalItems);
    document.getElementById('current-page-info').textContent =
        `Showing ${loaded} of ${galleryState.totalItems} files`;
}

function showLoading(show) {
    const loading = document.getElementById('loading');
    if (show) {
        loading.classList.remove('hidden');
        loading.style.display = 'block';
    } else {
        loading.classList.add('hidden');
        loading.style.display = 'none';
    }
}

function openModal(url) {
    const modal = document.getElementById('modal');
    const modalContent = document.getElementById('modal-content');
    modalContent.src = url;
    modal.classList.remove('hidden');
}

function closeModal() {
    const modal = document.getElementById('modal');
    modal.classList.add('hidden');
}

// Infinite scroll handler
function handleScroll() {
    if (galleryState.isLoading || !galleryState.hasMore) return;

    const scrollTop = window.scrollY || document.documentElement.scrollTop;
    const windowHeight = window.innerHeight;
    const documentHeight = document.documentElement.scrollHeight;

    // Load more when within 500px of bottom
    if (scrollTop + windowHeight >= documentHeight - 500) {
        fetchMedia();
    }
}

// Initialize gallery page
function initGallery() {
    console.log('🖼️ Gallery: Initializing with infinite scroll');

    // Reset state
    galleryState.offset = 0;
    galleryState.totalItems = 0;
    galleryState.isLoading = false;
    galleryState.hasMore = true;
    galleryState.profileCache.clear();
    galleryState.profileFetchQueue.clear();

    // Clear gallery
    const gallery = document.getElementById('gallery');
    if (gallery) gallery.innerHTML = '';

    // Hide end message
    const endMsg = document.getElementById('end-message');
    if (endMsg) endMsg.classList.add('hidden');

    // Fetch initial data
    fetchServerStats();
    fetchMedia();

    // Setup infinite scroll
    window.removeEventListener('scroll', handleScroll);
    window.addEventListener('scroll', handleScroll);
}

// Cleanup on page leave
function cleanupGallery() {
    window.removeEventListener('scroll', handleScroll);
    if (indexRelayWs) {
        indexRelayWs.close();
        indexRelayWs = null;
    }
}

// Handle htmx navigation
document.body.addEventListener('htmx:beforeSwap', cleanupGallery);

console.log('🖼️ Gallery script loaded');
