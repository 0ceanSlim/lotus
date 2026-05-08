// Blossom Auth Signer
// Creates and signs Blossom authentication events (kind 24242) for BUD operations

class BlossomSigner {
    constructor(sessionManager) {
        this.sessionManager = sessionManager;
    }

    /**
     * Create an unsigned Blossom auth event
     * @param {string} action - The action ("upload", "delete", etc.)
     * @param {string} hash - The sha256 hash (required for upload/delete)
     * @param {number} expiration - Optional expiration timestamp (defaults to 5 min from now)
     * @returns {object} Unsigned event object
     */
    createAuthEvent(action, hash, expiration = null) {
        if (!expiration) {
            expiration = Math.floor(Date.now() / 1000) + (5 * 60); // 5 minutes from now
        }

        const tags = [
            ["t", action],
            ["expiration", expiration.toString()]
        ];

        // Add x tag if hash is provided
        if (hash) {
            tags.push(["x", hash]);
        }

        return {
            kind: 24242,
            created_at: Math.floor(Date.now() / 1000),
            tags: tags,
            content: ""
        };
    }

    /**
     * Sign a Blossom auth event using the current session's signing method
     * @param {string} action - The action ("upload", "delete", etc.)
     * @param {string} hash - The sha256 hash
     * @param {number} expiration - Optional expiration timestamp
     * @returns {Promise<string>} The Authorization header value ("Nostr <base64>")
     */
    async signAuthEvent(action, hash, expiration = null) {
        if (!this.sessionManager || !this.sessionManager.isAuthenticated()) {
            throw new Error('Not authenticated');
        }

        const session = this.sessionManager.getSession();
        const event = this.createAuthEvent(action, hash, expiration);

        let signedEvent;

        switch (session.signingMethod) {
            case 'browser_extension':
                signedEvent = await this.signWithExtension(event);
                break;

            case 'amber':
                signedEvent = await this.signWithAmber(event);
                break;

            case 'encrypted_key':
                signedEvent = await this.signWithEncryptedKey(event);
                break;

            default:
                throw new Error(`Unknown signing method: ${session.signingMethod}`);
        }

        // Encode as base64 JSON
        const eventJSON = JSON.stringify(signedEvent);
        const base64Event = btoa(eventJSON);

        return `Nostr ${base64Event}`;
    }

    /**
     * Sign event using browser extension (NIP-07)
     * @param {object} event - Unsigned event
     * @returns {Promise<object>} Signed event
     */
    async signWithExtension(event) {
        if (!window.nostr) {
            throw new Error('No Nostr extension found. Please install Alby, nos2x, or another Nostr extension.');
        }

        try {
            const signedEvent = await window.nostr.signEvent(event);
            return signedEvent;
        } catch (error) {
            throw new Error(`Extension signing failed: ${error.message}`);
        }
    }

    /**
     * Sign event using Amber (NIP-55)
     * @param {object} event - Unsigned event
     * @returns {Promise<object>} Signed event
     */
    async signWithAmber(event) {
        // Amber signing via NIP-55 is more complex
        // For now, we'll throw an error and require implementation
        throw new Error('Amber signing for Blossom auth events is not yet implemented. Please use browser extension.');
    }

    /**
     * Sign event using encrypted private key
     * @param {object} event - Unsigned event
     * @returns {Promise<object>} Signed event
     */
    async signWithEncryptedKey(event) {
        // Check if we have an encrypted key
        if (!window.getEncryptedKey || !window.getEncryptedKey()) {
            throw new Error('No encrypted private key found. Please log in again with your private key.');
        }

        // Check if signing functions are available
        if (!window.promptForPassword || !window.signNostrEvent) {
            throw new Error('Signing functions not available. Please refresh the page.');
        }

        try {
            // Prompt user for password to decrypt the key
            const privateKey = await window.promptForPassword('Enter your password to sign the delete request:');

            // Sign the event with the decrypted private key
            const signedEvent = await window.signNostrEvent(event, privateKey);

            return signedEvent;
        } catch (error) {
            if (error.message.includes('cancelled')) {
                throw new Error('Signing cancelled by user');
            }
            throw new Error(`Failed to sign event: ${error.message}`);
        }
    }

    /**
     * Create an authenticated DELETE request
     * @param {string} hash - The sha256 hash to delete
     * @returns {Promise<Response>} Fetch response
     */
    async deleteBlob(hash) {
        const authHeader = await this.signAuthEvent('delete', hash);

        const response = await fetch(`/${hash}`, {
            method: 'DELETE',
            headers: {
                'Authorization': authHeader
            }
        });

        return response;
    }

    /**
     * Create an authenticated upload request headers
     * @param {string} hash - The sha256 hash of the file to upload
     * @returns {Promise<object>} Headers object with Authorization
     */
    async getUploadHeaders(hash) {
        const authHeader = await this.signAuthEvent('upload', hash);

        return {
            'Authorization': authHeader
        };
    }
}

// Create global instance
window.blossomSigner = null;

// Initialize when session manager is ready
try {
    if (window.sessionManager) {
        window.blossomSigner = new BlossomSigner(window.sessionManager);
    } else {
        document.addEventListener('DOMContentLoaded', function() {
            const checkSessionManager = () => {
                if (window.sessionManager) {
                    try {
                        window.blossomSigner = new BlossomSigner(window.sessionManager);
                        console.log('✅ Blossom signer initialized');
                    } catch (error) {
                        console.error('❌ Failed to initialize Blossom signer:', error);
                    }
                } else {
                    setTimeout(checkSessionManager, 50);
                }
            };
            checkSessionManager();
        });
    }

    console.log('🔐 Blossom signer loaded');
} catch (error) {
    console.error('❌ Failed to load Blossom signer:', error);
}
