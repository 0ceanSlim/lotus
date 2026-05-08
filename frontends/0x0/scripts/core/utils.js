// Utility functions for Blossom

// ========== Encryption/Decryption Utilities ==========

// Encrypt private key with password using AES-GCM
async function encryptPrivateKey(privateKey, password) {
    const encoder = new TextEncoder();
    const data = encoder.encode(privateKey);

    // Derive key from password using PBKDF2
    const salt = crypto.getRandomValues(new Uint8Array(16));
    const keyMaterial = await crypto.subtle.importKey(
        'raw',
        encoder.encode(password),
        'PBKDF2',
        false,
        ['deriveBits', 'deriveKey']
    );

    const key = await crypto.subtle.deriveKey(
        {
            name: 'PBKDF2',
            salt: salt,
            iterations: 100000,
            hash: 'SHA-256'
        },
        keyMaterial,
        { name: 'AES-GCM', length: 256 },
        false,
        ['encrypt']
    );

    // Encrypt with AES-GCM
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const encrypted = await crypto.subtle.encrypt(
        { name: 'AES-GCM', iv: iv },
        key,
        data
    );

    // Return salt + iv + encrypted data as base64
    const result = new Uint8Array(salt.length + iv.length + encrypted.byteLength);
    result.set(salt, 0);
    result.set(iv, salt.length);
    result.set(new Uint8Array(encrypted), salt.length + iv.length);

    return btoa(String.fromCharCode(...result));
}

// Decrypt private key with password
async function decryptPrivateKey(encryptedData, password) {
    const encoder = new TextEncoder();
    const decoder = new TextDecoder();

    // Decode base64
    const data = Uint8Array.from(atob(encryptedData), c => c.charCodeAt(0));

    // Extract salt, iv, and encrypted content
    const salt = data.slice(0, 16);
    const iv = data.slice(16, 28);
    const encrypted = data.slice(28);

    // Derive key from password
    const keyMaterial = await crypto.subtle.importKey(
        'raw',
        encoder.encode(password),
        'PBKDF2',
        false,
        ['deriveBits', 'deriveKey']
    );

    const key = await crypto.subtle.deriveKey(
        {
            name: 'PBKDF2',
            salt: salt,
            iterations: 100000,
            hash: 'SHA-256'
        },
        keyMaterial,
        { name: 'AES-GCM', length: 256 },
        false,
        ['decrypt']
    );

    // Decrypt
    const decrypted = await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: iv },
        key,
        encrypted
    );

    return decoder.decode(decrypted);
}

// Store encrypted private key
function storeEncryptedKey(encryptedKey) {
    sessionStorage.setItem('blossom_encrypted_key', encryptedKey);
}

// Get encrypted private key
function getEncryptedKey() {
    return sessionStorage.getItem('blossom_encrypted_key');
}

// Clear encrypted private key
function clearEncryptedKey() {
    sessionStorage.removeItem('blossom_encrypted_key');
}

// Convert nsec to hex private key
function nsecToHex(nsec) {
    if (!nsec.startsWith('nsec1')) {
        // Already hex
        return nsec;
    }

    // Bech32 decode
    const ALPHABET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l';
    const data = nsec.slice(5); // Remove 'nsec1'

    // Decode bech32 characters to 5-bit values
    const values = [];
    for (const char of data) {
        const idx = ALPHABET.indexOf(char);
        if (idx === -1) throw new Error('Invalid bech32 character');
        values.push(idx);
    }

    // Remove checksum (last 6 characters)
    const payload = values.slice(0, -6);

    // Convert 5-bit to 8-bit
    let acc = 0;
    let bits = 0;
    const result = [];
    for (const value of payload) {
        acc = (acc << 5) | value;
        bits += 5;
        while (bits >= 8) {
            bits -= 8;
            result.push((acc >> bits) & 0xff);
        }
    }

    return result.map(b => b.toString(16).padStart(2, '0')).join('');
}

// Get public key from private key
function getPublicKeyFromPrivate(privateKeyHex) {
    if (!window.NostrTools) {
        throw new Error('Nostr-tools library not loaded');
    }
    return window.NostrTools.getPublicKey(privateKeyHex);
}

// Sign a Nostr event with private key
async function signNostrEvent(event, privateKeyHex) {
    if (!window.NostrTools) {
        throw new Error('Nostr-tools library not loaded');
    }

    // Use nostr-tools to finalize and sign the event
    const signedEvent = window.NostrTools.finalizeEvent(event, privateKeyHex);
    return signedEvent;
}

// Prompt for password and return the decrypted private key
async function promptForPassword(message = 'Enter your password to sign:') {
    return new Promise((resolve, reject) => {
        // Create modal
        const modal = document.createElement('div');
        modal.id = 'password-prompt-modal';
        modal.className = 'fixed inset-0 z-[200] flex items-center justify-center p-4';
        modal.style.backgroundColor = 'rgba(0, 0, 0, 0.75)';

        modal.innerHTML = `
            <div class="bg-gray-800 border border-purple-500 rounded-lg w-full max-w-sm" onclick="event.stopPropagation()">
                <div class="p-6">
                    <h3 class="text-lg font-bold mb-4 text-center text-purple-400">🔐 Password Required</h3>
                    <p class="text-sm text-gray-300 mb-4 text-center">${message}</p>
                    <div class="space-y-4">
                        <input
                            type="password"
                            id="decrypt-password-input"
                            class="w-full bg-gray-700 px-3 py-2 rounded border border-gray-600 focus:border-purple-400 focus:outline-none"
                            placeholder="Enter password"
                            autofocus
                        />
                        <div class="flex gap-3">
                            <button id="decrypt-confirm-btn" class="flex-1 bg-purple-600 hover:bg-purple-700 px-4 py-2 rounded-lg font-medium transition-colors">
                                Confirm
                            </button>
                            <button id="decrypt-cancel-btn" class="flex-1 bg-gray-600 hover:bg-gray-700 px-4 py-2 rounded-lg font-medium transition-colors">
                                Cancel
                            </button>
                        </div>
                    </div>
                </div>
            </div>
        `;

        document.body.appendChild(modal);

        const input = document.getElementById('decrypt-password-input');
        const confirmBtn = document.getElementById('decrypt-confirm-btn');
        const cancelBtn = document.getElementById('decrypt-cancel-btn');

        input.focus();

        const cleanup = () => {
            modal.remove();
        };

        const handleConfirm = async () => {
            const password = input.value;
            if (!password) {
                input.classList.add('border-red-500');
                return;
            }

            try {
                const encryptedKey = getEncryptedKey();
                if (!encryptedKey) {
                    throw new Error('No encrypted key found');
                }

                const privateKey = await decryptPrivateKey(encryptedKey, password);
                cleanup();
                resolve(privateKey);
            } catch (error) {
                input.value = '';
                input.placeholder = 'Wrong password, try again';
                input.classList.add('border-red-500');
            }
        };

        confirmBtn.onclick = handleConfirm;
        cancelBtn.onclick = () => {
            cleanup();
            reject(new Error('Password entry cancelled'));
        };

        input.onkeypress = (e) => {
            if (e.key === 'Enter') handleConfirm();
        };

        modal.onclick = (e) => {
            if (e.target === modal) {
                cleanup();
                reject(new Error('Password entry cancelled'));
            }
        };
    });
}

// Make functions globally available
window.encryptPrivateKey = encryptPrivateKey;
window.decryptPrivateKey = decryptPrivateKey;
window.storeEncryptedKey = storeEncryptedKey;
window.getEncryptedKey = getEncryptedKey;
window.clearEncryptedKey = clearEncryptedKey;
window.nsecToHex = nsecToHex;
window.signNostrEvent = signNostrEvent;
window.promptForPassword = promptForPassword;
window.getPublicKeyFromPrivate = getPublicKeyFromPrivate;

// Show toast message
function showMessage(message, type = 'info', duration = 3000) {
    const toast = document.createElement('div');
    const bgColors = {
        success: 'bg-green-500',
        error: 'bg-red-500',
        warning: 'bg-yellow-500',
        info: 'bg-blue-500'
    };

    toast.className = `fixed top-4 right-4 ${bgColors[type] || bgColors.info} text-white px-6 py-3 rounded-lg shadow-lg z-50 transition-opacity`;
    toast.textContent = message;

    document.body.appendChild(toast);

    setTimeout(() => {
        toast.style.opacity = '0';
        setTimeout(() => toast.remove(), 300);
    }, duration);
}

// Show/hide login modal
window.showLoginModal = function() {
    const modal = document.getElementById('login-modal');
    if (modal) {
        modal.classList.remove('hidden');
        // Reset to default content
        showDefaultLoginContent();
    }
};

window.hideLoginModal = function() {
    const modal = document.getElementById('login-modal');
    if (modal) {
        modal.classList.add('hidden');
    }
};

// Show default login content
function showDefaultLoginContent() {
    const modalContent = document.getElementById('login-modal-content');
    if (!modalContent) return;

    modalContent.innerHTML = `
        <!-- Generate New Keys -->
        <div class="bg-gray-800 p-4 rounded-lg border border-gray-700 hover:border-green-500 transition-colors">
          <div class="flex items-start gap-3">
            <div class="text-3xl">✨</div>
            <div class="flex-1">
              <h4 class="font-bold mb-1 text-white">New to Nostr?</h4>
              <p class="text-sm text-gray-400 mb-2">Generate a new keypair and start using Blossom</p>
              <button
                onclick="generateNewKeys()"
                class="w-full bg-green-600 hover:bg-green-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
              >
                ✨ Generate Keys
              </button>
            </div>
          </div>
        </div>

        <!-- Browser Extension -->
        <div class="bg-gray-800 p-4 rounded-lg border border-gray-700 hover:border-purple-500 transition-colors">
          <div class="flex items-start gap-3">
            <div class="text-3xl">🔗</div>
            <div class="flex-1">
              <h4 class="font-bold mb-1 text-white">Browser Extension</h4>
              <p class="text-sm text-gray-400 mb-2">Use Alby, nos2x, or other Nostr extensions</p>
              <button
                onclick="loginWithExtension()"
                class="w-full bg-purple-600 hover:bg-purple-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
              >
                🔗 Extension
              </button>
            </div>
          </div>
        </div>

        <!-- Amber Signer -->
        <div class="bg-gray-800 p-4 rounded-lg border border-gray-700 hover:border-orange-500 transition-colors">
          <div class="flex items-start gap-3">
            <div class="text-3xl">📱</div>
            <div class="flex-1">
              <h4 class="font-bold mb-1 text-white">Amber Signer</h4>
              <p class="text-sm text-gray-400 mb-2">Login with Amber app on your mobile device</p>
              <button
                onclick="loginWithAmber()"
                class="w-full bg-orange-600 hover:bg-orange-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
              >
                📱 Amber
              </button>
            </div>
          </div>
        </div>

        <!-- Private Key -->
        <div class="bg-gray-800 p-4 rounded-lg border border-gray-700 hover:border-red-500 transition-colors">
          <div class="flex items-start gap-3">
            <div class="text-3xl">🗝️</div>
            <div class="flex-1">
              <h4 class="font-bold mb-1 text-white">Private Key</h4>
              <p class="text-sm text-gray-400 mb-2">Login with your nsec or hex private key</p>
              <button
                onclick="showKeyLogin()"
                class="w-full bg-red-600 hover:bg-red-700 text-white px-4 py-2 rounded-lg font-medium transition-colors"
              >
                🗝️ Private Key
              </button>
            </div>
          </div>
        </div>
    `;
}

// Hide generated keys modal
function hideGeneratedKeys() {
    const modal = document.getElementById('generated-keys-modal');
    if (modal) {
        modal.classList.add('hidden');
    }
}

console.log('✅ Utils loaded');
