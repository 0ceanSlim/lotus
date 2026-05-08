/**
 * Profile Manager
 * Fetches and manages Nostr profile metadata (kind 0 events)
 */

class ProfileManager {
  constructor() {
    this.profile = null;
    this.pubkey = null;
    this.npub = null;
  }

  /**
   * Initialize profile manager with user's pubkey
   * @param {string} pubkey - Hex public key
   * @param {string} npub - Bech32 npub
   * @param {boolean} skipRelayFetch - Skip relay fetching (for new accounts)
   */
  async init(pubkey, npub, skipRelayFetch = false) {
    this.pubkey = pubkey;
    this.npub = npub;

    // Check cache first
    const cached = this.getCachedProfile(pubkey);
    if (cached) {
      this.profile = cached;
      this.dispatchProfileUpdate();
      return cached;
    }

    // Skip relay fetch for new accounts (they have no events yet)
    if (skipRelayFetch) {
      console.log('⚡ Skipping relay fetch for new account');
      this.profile = {
        npub,
        pubkey,
        display_name: npub.slice(0, 10) + '...',
        name: null,
        picture: null,
        about: null
      };
      this.dispatchProfileUpdate();
      return this.profile;
    }

    // Fetch from relays via backend API
    try {
      const profile = await this.fetchProfile(pubkey);
      if (profile) {
        this.profile = { ...profile, npub, pubkey };
        this.cacheProfile(pubkey, this.profile);
        this.dispatchProfileUpdate();
        return this.profile;
      }
    } catch (error) {
      console.error('Failed to fetch profile:', error);
    }

    // Return minimal profile if fetch fails
    this.profile = {
      npub,
      pubkey,
      display_name: npub.slice(0, 10) + '...',
      name: null,
      picture: null,
      about: null
    };
    this.dispatchProfileUpdate();
    return this.profile;
  }

  /**
   * Fetch profile from backend API (with caching)
   * @param {string} pubkey - Hex public key
   * @returns {Promise<Object>} Profile metadata
   */
  async fetchProfile(pubkey) {
    try {
      // Use backend API which has caching
      const url = `/api/profile?npub=${this.npub}`;
      console.log('👤 Fetching profile from:', url);

      const response = await fetch(url);

      if (!response.ok) {
        console.warn('Failed to fetch profile from backend:', response.status);
        return null;
      }

      const data = await response.json();
      console.log('👤 Profile API response:', data);

      if (data.success && data.profile) {
        const profile = {
          display_name: data.profile.display_name || data.profile.DisplayName || '',
          name: data.profile.name || data.profile.Name || '',
          picture: data.profile.picture || data.profile.Picture || '',
          about: data.profile.about || data.profile.About || '',
          nip05: data.profile.nip05 || data.profile.Nip05 || '',
          lud16: data.profile.lud16 || data.profile.Lud16 || ''
        };
        console.log('👤 Parsed profile:', profile);
        return profile;
      }

      console.warn('👤 Profile API returned no profile data');
      return null;
    } catch (error) {
      console.error('Error fetching profile from backend:', error);
      return null;
    }
  }

  /**
   * Cache profile data in sessionStorage
   * @param {string} pubkey - Hex public key
   * @param {Object} profile - Profile data
   */
  cacheProfile(pubkey, profile) {
    try {
      sessionStorage.setItem(`profile_${pubkey}`, JSON.stringify({
        data: profile,
        timestamp: Date.now()
      }));
    } catch (error) {
      console.error('Failed to cache profile:', error);
    }
  }

  /**
   * Get cached profile from sessionStorage
   * @param {string} pubkey - Hex public key
   * @returns {Object|null} Cached profile or null
   */
  getCachedProfile(pubkey) {
    try {
      const cached = sessionStorage.getItem(`profile_${pubkey}`);
      if (cached) {
        const { data, timestamp } = JSON.parse(cached);
        // Cache valid for 1 hour
        if (Date.now() - timestamp < 3600000) {
          return data;
        }
      }
    } catch (error) {
      console.error('Failed to get cached profile:', error);
    }
    return null;
  }

  /**
   * Dispatch profile update event
   */
  dispatchProfileUpdate() {
    window.dispatchEvent(new CustomEvent('profile-updated', {
      detail: this.profile
    }));
  }

  /**
   * Get current profile
   * @returns {Object|null} Current profile data
   */
  getProfile() {
    // If profile in memory, return it
    if (this.profile) {
      return this.profile;
    }

    // Try to restore from cache using pubkey
    if (this.pubkey) {
      const cached = this.getCachedProfile(this.pubkey);
      if (cached) {
        this.profile = cached;
        return cached;
      }
    }

    // Try to get pubkey from session manager and check cache
    if (window.sessionManager) {
      const session = window.sessionManager.getSession();
      if (session) {
        const pubkey = session.pubkey || session.publicKey;
        if (pubkey) {
          const cached = this.getCachedProfile(pubkey);
          if (cached) {
            this.profile = cached;
            this.pubkey = pubkey;
            this.npub = session.npub;
            return cached;
          }
        }
      }
    }

    return null;
  }
}

// Create global profile manager instance
window.profileManager = new ProfileManager();

console.log('👤 ProfileManager loaded');
