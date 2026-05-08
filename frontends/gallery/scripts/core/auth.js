// Authentication functions for Blossom using SessionManager

// Initialize authentication with session manager integration
function initializeAuthentication() {
    if (!window.sessionManager) {
        console.error('SessionManager not available for authentication');
        return;
    }

    // Listen for profile updates
    window.addEventListener('profile-updated', (event) => {
        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }
    });

    // Set up session manager event listeners
    window.sessionManager.on('sessionReady', async (sessionData) => {
        console.log('Session ready', sessionData);

        // Load profile for existing session
        const session = window.sessionManager?.getSession();
        if (session && window.profileManager) {
            try {
                const pubkey = session.pubkey || session.publicKey;
                await window.profileManager.init(pubkey, session.npub, false);
            } catch (error) {
                console.error('Failed to fetch profile on session ready:', error);
            }
        }

        // Initialize relay manager (non-blocking)
        if (session && window.relayManager) {
            const pk = session.pubkey || session.publicKey;
            window.relayManager.initialize(pk).catch(error => {
                console.error('Failed to initialize relay manager:', error);
            });
        }

        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }
    });

    window.sessionManager.on('authenticationRequired', () => {
        console.log('Authentication required');
        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }
    });

    window.sessionManager.on('sessionExpired', () => {
        console.log('Session expired');
        showMessage('Your session has expired. Please log in again.', 'warning');
        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }
    });

    window.sessionManager.on('authenticationSuccess', async (data) => {
        const method = typeof data === 'string' ? data : data.method;
        const isNewAccount = typeof data === 'object' ? data.isNewAccount : false;

        console.log(`Authentication successful via ${method}${isNewAccount ? ' (new account)' : ''}`);

        // Hide loading modal
        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        showMessage(`Logged in successfully via ${method}`, 'success');

        // Initialize profile manager
        const session = window.sessionManager?.getSession();
        if (session && window.profileManager) {
            try {
                const pubkey = session.pubkey || session.publicKey;
                await window.profileManager.init(pubkey, session.npub, isNewAccount);
            } catch (error) {
                console.error('Failed to fetch profile:', error);
            }
        }

        // Initialize relay manager (non-blocking)
        if (session && window.relayManager) {
            const pk = session.pubkey || session.publicKey;
            window.relayManager.initialize(pk).catch(error => {
                console.error('Failed to initialize relay manager:', error);
            });
        }

        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }

        if (typeof hideLoginModal === 'function') {
            hideLoginModal();
        }
    });

    window.sessionManager.on('authenticationFailed', ({ method, error }) => {
        console.error(`Authentication failed via ${method}:`, error);

        // Hide loading modal
        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        showMessage(`Login failed via ${method}: ${error}`, 'error');
    });

    window.sessionManager.on('sessionError', (error) => {
        console.error('Session error:', error);
        showMessage('Session error: ' + error.message, 'error');
    });

    window.sessionManager.on('loggedOut', () => {
        console.log('Logged out');
        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }
    });

    // Check if session already exists (sessionReady may have fired before listeners were set up)
    if (window.sessionManager.isAuthenticated()) {
        console.log('Session already active, initializing profile and relays');
        const session = window.sessionManager.getSession();
        if (session) {
            const pubkey = session.pubkey || session.publicKey;

            // Initialize profile manager
            if (window.profileManager && pubkey) {
                window.profileManager.init(pubkey, session.npub, false).catch(error => {
                    console.error('Failed to fetch profile:', error);
                });
            }

            // Initialize relay manager
            if (window.relayManager && pubkey) {
                window.relayManager.initialize(pubkey).catch(error => {
                    console.error('Failed to initialize relay manager:', error);
                });
            }

            // Update UI
            if (typeof updateLoginButton === 'function') {
                setTimeout(updateLoginButton, 100);
            }
        }
    }
}

// Login with browser extension
window.loginWithExtension = async function() {
    if (!window.nostr) {
        showMessage('No Nostr extension found. Please install Alby or nos2x.', 'error');
        return;
    }

    try {
        if (typeof showLoadingModal === 'function') {
            showLoadingModal('Connecting to browser extension...');
        }

        const publicKey = await window.nostr.getPublicKey();

        if (!publicKey || publicKey.length !== 64) {
            throw new Error('Invalid public key received from extension');
        }

        console.log('Extension returned public key:', publicKey);

        if (typeof showLoadingModal === 'function') {
            showLoadingModal('Creating session...');
        }

        const sessionRequest = {
            public_key: publicKey,
            signing_method: 'browser_extension',
            mode: 'write'
        };

        const response = await fetch('/api/auth/login', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(sessionRequest)
        });

        if (!response.ok) {
            const errorData = await response.json().catch(() => null);
            const errorMsg = errorData?.message || `HTTP ${response.status}`;
            throw new Error(`Login failed: ${errorMsg}`);
        }

        const result = await response.json();

        if (!result.success) {
            throw new Error(result.message || 'Login failed');
        }

        console.log('Extension login successful');

        // Refresh session manager state
        if (window.sessionManager) {
            await window.sessionManager.checkExistingSession();
        }

        window.nostrExtensionConnected = true;

        // Hide modals
        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        if (typeof hideLoginModal === 'function') {
            hideLoginModal();
        }

        showMessage('Connected via browser extension!', 'success');

        // Initialize profile and relays
        const session = window.sessionManager?.getSession();
        if (session) {
            const pubkey = session.pubkey || session.publicKey;

            if (window.profileManager && pubkey) {
                window.profileManager.init(pubkey, session.npub, false).catch(error => {
                    console.error('Failed to fetch profile:', error);
                });
            }

            if (window.relayManager && pubkey) {
                window.relayManager.initialize(pubkey).catch(error => {
                    console.error('Failed to initialize relay manager:', error);
                });
            }
        }

        // Update UI immediately, profile-updated event will update again with full data
        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }

    } catch (error) {
        console.error('Extension login error:', error);

        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        showMessage('Extension login failed: ' + error.message, 'error');
    }
};

// Login with Amber using NIP-55 protocol
window.loginWithAmber = function() {
    // Check if running on localhost
    if (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1') {
        showMessage('Amber login requires a local IP address (like 192.168.x.x), not localhost.', 'error', 10000);
        return;
    }

    if (typeof showLoadingModal === 'function') {
        showLoadingModal('Opening Amber app...');
    }

    // Set up callback listener BEFORE opening Amber
    setupAmberCallbackListener();

    // Generate proper callback URL
    const callbackUrl = `${window.location.origin}/api/auth/amber-callback?event=`;

    // Use NIP-55 nostrsigner URL format
    const amberUrl = `nostrsigner:?compressionType=none&returnType=signature&type=get_public_key&callbackUrl=${encodeURIComponent(callbackUrl)}&appName=${encodeURIComponent('Blossom')}`;

    console.log('Opening Amber with URL:', amberUrl);

    try {
        // Try anchor element click (most reliable on mobile)
        const anchor = document.createElement('a');
        anchor.href = amberUrl;
        anchor.target = '_blank';
        anchor.style.display = 'none';
        document.body.appendChild(anchor);
        anchor.click();

        setTimeout(() => {
            if (document.body.contains(anchor)) {
                document.body.removeChild(anchor);
            }
        }, 100);

        // Set timeout in case user doesn't complete the flow
        setTimeout(() => {
            if (!amberCallbackReceived) {
                if (typeof hideLoadingModal === 'function') {
                    hideLoadingModal();
                }
                showMessage('Amber connection timed out. Make sure Amber is installed.', 'error');
            }
        }, 60000);

    } catch (error) {
        console.error('Error opening Amber:', error);

        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        showMessage('Amber login failed: ' + error.message, 'error');
    }
};

// Set up Amber callback listener
function setupAmberCallbackListener() {
    const handleVisibilityChange = () => {
        if (!document.hidden && !amberCallbackReceived) {
            setTimeout(checkForAmberCallback, 500);
        }
    };

    const handleFocus = () => {
        if (!amberCallbackReceived) {
            setTimeout(checkForAmberCallback, 500);
        }
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);
    window.addEventListener('focus', handleFocus);

    setTimeout(checkForAmberCallback, 1000);

    // Clean up listeners after timeout
    setTimeout(() => {
        document.removeEventListener('visibilitychange', handleVisibilityChange);
        window.removeEventListener('focus', handleFocus);
    }, 65000);
}

// Check for Amber callback
function checkForAmberCallback() {
    console.log('Checking for Amber callback...');

    const amberResult = localStorage.getItem('amber_callback_result');
    if (amberResult) {
        try {
            console.log('Found Amber result in localStorage:', amberResult);
            localStorage.removeItem('amber_callback_result');
            const data = JSON.parse(amberResult);
            amberCallbackReceived = true;
            handleAmberCallbackData(data);
        } catch (error) {
            console.error('Failed to parse stored Amber result:', error);
        }
    }
}

// Handle Amber callback data
async function handleAmberCallbackData(data) {
    try {
        if (data.error) {
            throw new Error(data.error);
        }

        console.log('Amber login completed successfully');

        showMessage('Connected via Amber!', 'success');
        window.amberConnected = true;

        // Refresh session manager state
        if (window.sessionManager) {
            await window.sessionManager.checkExistingSession();
        }

        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        if (typeof hideLoginModal === 'function') {
            hideLoginModal();
        }

        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }

    } catch (error) {
        console.error('Error processing Amber callback data:', error);

        if (typeof hideLoadingModal === 'function') {
            hideLoadingModal();
        }

        showMessage('Amber login failed: ' + error.message, 'error');
    }
}

// Logout function
window.logout = async function() {
    if (!window.sessionManager) {
        showMessage('Session manager not available', 'error');
        return;
    }

    try {
        showMessage('Logging out...', 'info');
        await window.sessionManager.logout();
        showMessage('Successfully logged out', 'success');

        if (typeof updateLoginButton === 'function') {
            updateLoginButton();
        }

        setTimeout(() => {
            if (window.htmx) {
                htmx.ajax('GET', '/', {target: '#main-content', swap: 'innerHTML'});
                history.pushState({}, '', '/');
            } else {
                window.location.href = '/';
            }
        }, 1000);
    } catch (error) {
        console.error('Logout error:', error);
        showMessage('Logout failed: ' + error.message, 'error');
    }
};

// Initialize authentication system when DOM is loaded
document.addEventListener('DOMContentLoaded', function() {
    console.log('Authentication system loading...');

    const checkSessionManager = () => {
        if (window.sessionManager) {
            console.log('SessionManager found, initializing authentication');
            initializeAuthentication();
        } else {
            console.log('Waiting for SessionManager...');
            setTimeout(checkSessionManager, 100);
        }
    };

    checkSessionManager();
});

console.log('Authentication system loaded');
