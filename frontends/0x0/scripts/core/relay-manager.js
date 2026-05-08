// Relay Manager for Blossom
// Manages 3 types of relays: App relays, User relays (mailboxes), and Index relays
// Based on grain client relay management patterns

class RelayManager {
    constructor() {
        // Hardcoded index relays for fetching kind 0 and kind 10002
        this.indexRelays = [
            'wss://purplepag.es/'
        ];

        // User's mailbox relays (from kind 10002)
        this.userRelays = {
            read: [],
            write: [],
            both: []
        };

        // App relays (starts as copy of user relays, can be modified in settings)
        this.appRelays = [];

        // WebSocket connections
        this.connections = new Map();

        // Event listeners
        this.eventListeners = new Map();

        // Subscription management
        this.subscriptions = new Map();
    }

    /**
     * Initialize relay manager after login
     * @param {string} pubkey - User's public key
     * @returns {Promise<void>}
     */
    async initialize(pubkey) {
        console.log('🔌 Relay Manager: Initializing for pubkey', pubkey);

        try {
            // 1. Connect to index relays first
            console.log('Step 1: Connecting to index relays...');
            await this.connectToIndexRelays();

            // 2. Fetch user's mailboxes (kind 10002)
            console.log('Step 2: Fetching mailboxes...');
            const mailboxes = await this.fetchUserMailboxes(pubkey);
            if (mailboxes) {
                this.userRelays = mailboxes;
                console.log('📬 User mailboxes loaded:', this.userRelays);
            } else {
                console.log('📬 No mailboxes found, using empty defaults');
                this.userRelays = { read: [], write: [], both: [] };
            }

            // 3. Set app relays to user's mailboxes initially
            console.log('Step 3: Setting app relays...');
            this.appRelays = this.getUserRelayList();
            if (this.appRelays.length === 0) {
                console.log('⚠️ No user relays found, using index relays as fallback');
                this.appRelays = [...this.indexRelays];
            }

            // 4. Save to session storage
            console.log('Step 4: Saving to storage...');
            this.saveToStorage();

            // 5. Connect to app relays
            console.log('Step 5: Connecting to app relays...');
            await this.connectToAppRelays();

            console.log('✅ Relay Manager initialized successfully');
            this.emit('initialized', { userRelays: this.userRelays, appRelays: this.appRelays });
        } catch (error) {
            console.error('❌ Failed to initialize relay manager:', error);
            // Don't throw - allow app to continue even if relays fail
            this.emit('initializationFailed', error);
        }
    }

    /**
     * Connect to hardcoded index relays
     * @returns {Promise<void>}
     */
    async connectToIndexRelays() {
        console.log('🔌 Connecting to index relays:', this.indexRelays);

        const promises = this.indexRelays.map(url => this.connectToRelay(url));
        await Promise.allSettled(promises);
    }

    /**
     * Connect to app relays
     * @returns {Promise<void>}
     */
    async connectToAppRelays() {
        console.log('🔌 Connecting to app relays:', this.appRelays);

        const promises = this.appRelays.map(url => this.connectToRelay(url));
        await Promise.allSettled(promises);
    }

    /**
     * Connect to a single relay
     * @param {string} url - Relay WebSocket URL
     * @returns {Promise<WebSocket>}
     */
    connectToRelay(url) {
        return new Promise((resolve, reject) => {
            // Check if already connected
            if (this.connections.has(url)) {
                const existing = this.connections.get(url);
                if (existing.readyState === WebSocket.OPEN) {
                    console.log('Already connected to', url);
                    resolve(existing);
                    return;
                }
            }

            console.log('Connecting to relay:', url);

            const ws = new WebSocket(url);
            const timeout = setTimeout(() => {
                ws.close();
                reject(new Error(`Connection timeout: ${url}`));
            }, 5000);

            ws.onopen = () => {
                clearTimeout(timeout);
                console.log('✅ Connected to relay:', url);
                this.connections.set(url, ws);
                resolve(ws);
            };

            ws.onerror = (error) => {
                clearTimeout(timeout);
                console.error('❌ Failed to connect to relay:', url, error);
                reject(error);
            };

            ws.onmessage = (event) => {
                this.handleMessage(url, event.data);
            };

            ws.onclose = () => {
                console.log('🔌 Disconnected from relay:', url);
                this.connections.delete(url);
            };
        });
    }

    /**
     * Handle incoming WebSocket messages
     * @param {string} relayUrl - Relay URL
     * @param {string} data - Message data
     */
    handleMessage(relayUrl, data) {
        try {
            const message = JSON.parse(data);
            const [type, ...rest] = message;

            switch (type) {
                case 'EVENT':
                    const [subId, event] = rest;
                    this.handleEvent(subId, event, relayUrl);
                    break;

                case 'EOSE':
                    const [eoseSubId] = rest;
                    this.handleEOSE(eoseSubId, relayUrl);
                    break;

                case 'NOTICE':
                    const [notice] = rest;
                    console.log(`📢 Notice from ${relayUrl}:`, notice);
                    break;

                case 'OK':
                    const [eventId, success, msg] = rest;
                    console.log(`${success ? '✅' : '❌'} Event ${eventId} on ${relayUrl}:`, msg);
                    break;

                default:
                    console.log('Unknown message type:', type);
            }
        } catch (error) {
            console.error('Failed to parse message:', error, data);
        }
    }

    /**
     * Handle EVENT message
     */
    handleEvent(subId, event, relayUrl) {
        const sub = this.subscriptions.get(subId);
        if (sub && sub.onEvent) {
            sub.onEvent(event, relayUrl);
        }
    }

    /**
     * Handle EOSE (End of Stored Events) message
     */
    handleEOSE(subId, relayUrl) {
        const sub = this.subscriptions.get(subId);
        if (sub && sub.onEOSE) {
            sub.onEOSE(relayUrl);
        }
    }

    /**
     * Subscribe to events from relays
     * @param {Array<string>} relays - Relay URLs
     * @param {object} filters - Nostr filters
     * @param {function} onEvent - Event callback
     * @param {function} onEOSE - EOSE callback
     * @returns {string} Subscription ID
     */
    subscribe(relays, filters, onEvent, onEOSE) {
        const subId = 'sub_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);

        this.subscriptions.set(subId, {
            relays,
            filters,
            onEvent,
            onEOSE
        });

        // Send REQ to each relay
        const reqMessage = JSON.stringify(['REQ', subId, ...filters]);

        for (const relayUrl of relays) {
            const ws = this.connections.get(relayUrl);
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(reqMessage);
                console.log(`📡 Sent subscription ${subId} to ${relayUrl}`);
            } else {
                console.warn(`Cannot subscribe to ${relayUrl} - not connected`);
            }
        }

        return subId;
    }

    /**
     * Unsubscribe from events
     * @param {string} subId - Subscription ID
     */
    unsubscribe(subId) {
        const sub = this.subscriptions.get(subId);
        if (!sub) return;

        const closeMessage = JSON.stringify(['CLOSE', subId]);

        for (const relayUrl of sub.relays) {
            const ws = this.connections.get(relayUrl);
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(closeMessage);
            }
        }

        this.subscriptions.delete(subId);
    }

    /**
     * Fetch user's mailboxes (kind 10002)
     * @param {string} pubkey - User's public key
     * @returns {Promise<object>} Mailboxes object
     */
    fetchUserMailboxes(pubkey) {
        return new Promise((resolve, reject) => {
            const timeout = setTimeout(() => {
                this.unsubscribe(subId);
                console.log('⏱️ Timeout fetching mailboxes, using defaults');
                resolve(null);
            }, 5000);

            let latestEvent = null;
            const eoseRelays = new Set();

            const subId = this.subscribe(
                this.indexRelays,
                [{ kinds: [10002], authors: [pubkey], limit: 1 }],
                (event) => {
                    // Keep the latest event
                    if (!latestEvent || event.created_at > latestEvent.created_at) {
                        latestEvent = event;
                    }
                },
                (relayUrl) => {
                    eoseRelays.add(relayUrl);

                    // If we got event and EOSE from at least one relay, we're done
                    if (latestEvent) {
                        clearTimeout(timeout);
                        this.unsubscribe(subId);
                        const mailboxes = this.parseMailboxEvent(latestEvent);
                        resolve(mailboxes);
                    }
                }
            );
        });
    }

    /**
     * Parse kind 10002 event into mailboxes object
     * @param {object} event - Kind 10002 event
     * @returns {object} Mailboxes {read: [], write: [], both: []}
     */
    parseMailboxEvent(event) {
        const mailboxes = {
            read: [],
            write: [],
            both: []
        };

        if (!event || !event.tags) return mailboxes;

        for (const tag of event.tags) {
            if (tag[0] === 'r') {
                const url = tag[1];
                const marker = tag[2];

                if (marker === 'read') {
                    mailboxes.read.push(url);
                } else if (marker === 'write') {
                    mailboxes.write.push(url);
                } else {
                    // No marker or other marker = both read and write
                    mailboxes.both.push(url);
                }
            }
        }

        return mailboxes;
    }

    /**
     * Get combined list of user relay URLs
     * @returns {Array<string>} All user relay URLs
     */
    getUserRelayList() {
        return [
            ...this.userRelays.read,
            ...this.userRelays.write,
            ...this.userRelays.both
        ];
    }

    /**
     * Update app relays
     * @param {Array<string>} relays - New app relay list
     */
    async setAppRelays(relays) {
        // Disconnect from old relays not in new list
        const toDisconnect = this.appRelays.filter(url => !relays.includes(url));
        for (const url of toDisconnect) {
            this.disconnectFromRelay(url);
        }

        this.appRelays = relays;
        this.saveToStorage();

        // Connect to new relays
        await this.connectToAppRelays();
    }

    /**
     * Update index relays
     * @param {Array<string>} relays - New index relay list
     */
    setIndexRelays(relays) {
        this.indexRelays = relays;
        this.saveToStorage();
    }

    /**
     * Disconnect from a relay
     */
    disconnectFromRelay(url) {
        const ws = this.connections.get(url);
        if (ws) {
            ws.close();
            this.connections.delete(url);
        }
    }

    /**
     * Save relay configuration to session storage
     */
    saveToStorage() {
        const config = {
            userRelays: this.userRelays,
            appRelays: this.appRelays,
            indexRelays: this.indexRelays,
            timestamp: Date.now()
        };
        sessionStorage.setItem('relay_config', JSON.stringify(config));
    }

    /**
     * Load relay configuration from session storage
     * @returns {boolean} True if loaded successfully
     */
    loadFromStorage() {
        const stored = sessionStorage.getItem('relay_config');
        if (!stored) return false;

        try {
            const config = JSON.parse(stored);

            // Check if not too old (24 hours)
            if (Date.now() - config.timestamp > 24 * 60 * 60 * 1000) {
                return false;
            }

            this.userRelays = config.userRelays || { read: [], write: [], both: [] };
            this.appRelays = config.appRelays || [];
            this.indexRelays = config.indexRelays || this.indexRelays; // Keep defaults if not set

            console.log('📦 Loaded relay config from storage');
            return true;
        } catch (error) {
            console.error('Failed to load relay config:', error);
            return false;
        }
    }

    /**
     * Get all relay info
     */
    getRelayInfo() {
        // If no user/app relays in memory, try to restore from storage
        if ((!this.userRelays || (this.userRelays.read.length === 0 && this.userRelays.write.length === 0 && this.userRelays.both.length === 0)) &&
            (!this.appRelays || this.appRelays.length === 0)) {
            this.loadFromStorage();
        }

        return {
            index: this.indexRelays,
            user: this.userRelays,
            app: this.appRelays,
            connected: Array.from(this.connections.keys())
        };
    }

    /**
     * Event system
     */
    on(eventName, callback) {
        if (!this.eventListeners.has(eventName)) {
            this.eventListeners.set(eventName, []);
        }
        this.eventListeners.get(eventName).push(callback);
    }

    emit(eventName, data) {
        if (this.eventListeners.has(eventName)) {
            this.eventListeners.get(eventName).forEach(callback => {
                try {
                    callback(data);
                } catch (error) {
                    console.error(`Error in ${eventName} event handler:`, error);
                }
            });
        }
    }

    /**
     * Cleanup all connections
     */
    cleanup() {
        console.log('🧹 Cleaning up relay manager');

        // Close all subscriptions
        for (const subId of this.subscriptions.keys()) {
            this.unsubscribe(subId);
        }

        // Close all connections
        for (const ws of this.connections.values()) {
            ws.close();
        }

        this.connections.clear();
    }
}

// Create global instance
try {
    window.relayManager = new RelayManager();
    console.log('🔌 Relay Manager loaded');
} catch (error) {
    console.error('❌ Failed to create RelayManager:', error);
    // Create a stub so other code doesn't break
    window.relayManager = {
        initialize: () => Promise.resolve(),
        getRelayInfo: () => ({ index: [], user: { read: [], write: [], both: [] }, app: [], connected: [] }),
        setAppRelays: () => Promise.resolve(),
        setIndexRelays: () => {},
        getUserRelayList: () => []
    };
}
