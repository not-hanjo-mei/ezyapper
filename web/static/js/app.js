// EZyapper Dashboard JavaScript

const API_BASE = '/api';
const AUTH_HEADER_KEY = 'ezyapper.webui.authHeader';
const AUTH_USER_KEY = 'ezyapper.webui.username';
const API_TIMEOUT_MS = 10000;

let config = {};
let authHeader = sessionStorage.getItem(AUTH_HEADER_KEY) || '';
let authUser = sessionStorage.getItem(AUTH_USER_KEY) || '';

document.addEventListener('DOMContentLoaded', () => {
    initTabs();
    initAuthControls();
    updateAuthUI();
    loadHealthTag();

    if (authHeader) {
        bootstrapDashboard();
    } else {
        showToast('Login required for API access.', 'info');
    }

    setInterval(() => {
        if (authHeader) {
            loadStats();
        }
    }, 30000);
});

async function bootstrapDashboard() {
    try {
        await Promise.all([
            loadConfig(),
            loadStats(),
            loadPlugins(),
            loadBlacklist(),
            loadWhitelist(),
        ]);
    } catch (error) {
        console.error('Bootstrap failed:', error);
    }
}

function initAuthControls() {
    document.getElementById('loginBtn')?.addEventListener('click', async () => {
        const ok = await ensureAuth(true);
        if (!ok) {
            return;
        }

        try {
            await loadConfig();
            await bootstrapDashboard();
            showToast('Login successful.', 'success');
        } catch (error) {
            clearAuth(false);
            showToast('Login failed: invalid credentials.', 'error');
        }
    });

    document.getElementById('logoutBtn')?.addEventListener('click', () => {
        clearAuth(true);
    });
}

function updateAuthUI() {
    const usernameNode = document.getElementById('username');
    const loginBtn = document.getElementById('loginBtn');
    const logoutBtn = document.getElementById('logoutBtn');

    const loggedIn = authHeader !== '';
    if (usernameNode) {
        usernameNode.textContent = loggedIn ? authUser : 'guest';
    }

    if (loginBtn) {
        loginBtn.textContent = loggedIn ? 'Re-Login' : 'Login';
    }

    if (logoutBtn) {
        logoutBtn.disabled = !loggedIn;
    }
}

function clearAuth(showNotice) {
    authHeader = '';
    authUser = '';

    sessionStorage.removeItem(AUTH_HEADER_KEY);
    sessionStorage.removeItem(AUTH_USER_KEY);
    updateAuthUI();

    if (showNotice) {
        showToast('Logged out.', 'success');
    }
}

async function ensureAuth(forcePrompt = false) {
    if (authHeader && !forcePrompt) {
        return true;
    }

    const suggestedUser = authUser || 'admin';
    const usernameInput = window.prompt('WebUI username', suggestedUser);
    if (usernameInput === null) {
        return false;
    }

    const username = usernameInput.trim();
    if (!username) {
        showToast('Username cannot be empty.', 'error');
        return false;
    }

    const password = window.prompt('WebUI password');
    if (password === null) {
        return false;
    }

    authHeader = `Basic ${encodeBase64(`${username}:${password}`)}`;
    authUser = username;

    sessionStorage.setItem(AUTH_HEADER_KEY, authHeader);
    sessionStorage.setItem(AUTH_USER_KEY, authUser);
    updateAuthUI();

    return true;
}

function encodeBase64(value) {
    try {
        return btoa(value);
    } catch (_error) {
        const bytes = new TextEncoder().encode(value);
        let binary = '';
        for (const b of bytes) {
            binary += String.fromCharCode(b);
        }
        return btoa(binary);
    }
}

function initTabs() {
    const navLinks = document.querySelectorAll('.nav-link');
    navLinks.forEach((link) => {
        link.addEventListener('click', (e) => {
            e.preventDefault();
            const tabName = link.dataset.tab;
            showTab(tabName);

            navLinks.forEach((l) => l.classList.remove('active'));
            link.classList.add('active');

            if (tabName === 'dashboard') {
                loadStats();
            }
            if (tabName === 'channels') {
                loadBlacklist();
                loadWhitelist();
            }
            if (tabName === 'plugins') {
                loadPlugins();
            }
            if (tabName === 'logs') {
                loadLogs();
            }
        });
    });
}

function showTab(tabName) {
    const tabs = document.querySelectorAll('.tab-content');
    tabs.forEach((tab) => tab.classList.remove('active'));

    const activeTab = document.getElementById(`${tabName}-tab`);
    if (activeTab) {
        activeTab.classList.add('active');
    }
}

async function apiCall(endpoint, method = 'GET', data = null, allowReauth = true) {
    const hasAuth = await ensureAuth(false);
    if (!hasAuth) {
        throw new Error('authentication required');
    }

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), API_TIMEOUT_MS);

    const options = {
        method,
        headers: {
            'Content-Type': 'application/json',
            Authorization: authHeader,
        },
        signal: controller.signal,
    };

    if (data !== null) {
        options.body = JSON.stringify(data);
    }

    try {
        const response = await fetch(`${API_BASE}${endpoint}`, options);
        clearTimeout(timeoutId);

        if (response.status === 401) {
            clearAuth(false);
            if (allowReauth && (await ensureAuth(true))) {
                return apiCall(endpoint, method, data, false);
            }
            throw new Error('unauthorized');
        }

        if (!response.ok) {
            throw new Error(await readErrorResponse(response));
        }

        if (response.status === 204) {
            return {};
        }

        const rawText = await response.text();
        if (!rawText) {
            return {};
        }
        return JSON.parse(rawText);
    } catch (error) {
        clearTimeout(timeoutId);

        if (error.name === 'AbortError') {
            console.error('API timeout:', endpoint);
            throw new Error('request timed out');
        }

        console.error('API call failed:', error);
        throw error;
    }
}

async function readErrorResponse(response) {
    try {
        const payload = await response.json();
        if (payload && typeof payload.error === 'string' && payload.error.trim() !== '') {
            return payload.error;
        }
    } catch (_error) {
        // Ignore payload decode errors and use status text fallback.
    }

    if (response.statusText) {
        return `${response.status} ${response.statusText}`;
    }
    return `http error ${response.status}`;
}

async function loadConfig() {
    try {
        config = await apiCall('/config');

        document.getElementById('bot-name').value = config.discord?.bot_name || '';
        document.getElementById('reply-percentage').value = ((config.discord?.reply_percentage || 0) * 100).toFixed(0);
        document.getElementById('cooldown').value = config.discord?.cooldown_seconds || 0;

        document.getElementById('model').value = config.ai?.model || '';
        document.getElementById('vision-model').value = config.ai?.vision_model || '';
        document.getElementById('max-tokens').value = config.ai?.max_tokens || 1024;
        document.getElementById('temperature').value = config.ai?.temperature || 0.8;
        document.getElementById('system-prompt').value = config.ai?.system_prompt || '';
    } catch (error) {
        showToast(`Failed to load config: ${error.message}`, 'error');
    }
}

async function loadStats() {
    try {
        const payload = await apiCall('/stats');
        const stats = payload.stats || {};

        document.getElementById('stat-memories').textContent = stats.total_memories ?? 0;
        document.getElementById('stat-users').textContent = stats.total_users ?? 0;
        document.getElementById('stat-messages').textContent = stats.total_messages ?? 0;
        document.getElementById('stat-uptime').textContent = formatDuration(payload.uptime ?? 0);
    } catch (error) {
        showToast(`Failed to load stats: ${error.message}`, 'error');
    }
}

function formatDuration(secondsValue) {
    const total = Number(secondsValue);
    if (!Number.isFinite(total) || total < 0) {
        return '0s';
    }

    const seconds = Math.floor(total % 60);
    const minutes = Math.floor((total / 60) % 60);
    const hours = Math.floor((total / 3600) % 24);
    const days = Math.floor(total / 86400);

    const parts = [];
    if (days > 0) {
        parts.push(`${days}d`);
    }
    if (hours > 0 || days > 0) {
        parts.push(`${hours}h`);
    }
    if (minutes > 0 || hours > 0 || days > 0) {
        parts.push(`${minutes}m`);
    }
    parts.push(`${seconds}s`);

    return parts.join(' ');
}

async function loadPlugins() {
    const pluginsList = document.getElementById('plugins-list');
    if (!pluginsList) {
        return;
    }

    try {
        const data = await apiCall('/plugins');
        pluginsList.innerHTML = '';

        if (!Array.isArray(data.plugins) || data.plugins.length === 0) {
            pluginsList.textContent = 'No plugins found.';
            return;
        }

        data.plugins.forEach((plugin) => {
            pluginsList.appendChild(createPluginCard(plugin));
        });
    } catch (error) {
        pluginsList.textContent = 'Failed to load plugins.';
        showToast(`Failed to load plugins: ${error.message}`, 'error');
    }
}

function createPluginCard(plugin) {
    const card = document.createElement('div');
    card.className = 'plugin-card';

    const title = document.createElement('h4');
    title.textContent = plugin.name || 'Unnamed plugin';

    const description = document.createElement('p');
    description.textContent = plugin.description || 'No description provided.';

    const meta = document.createElement('p');
    const author = plugin.author || 'Unknown';
    meta.textContent = `Author: ${author} | Priority: ${plugin.priority ?? 0}`;

    const status = document.createElement('span');
    status.className = `plugin-status ${plugin.enabled ? 'enabled' : 'disabled'}`;
    status.textContent = plugin.enabled ? 'Enabled' : 'Disabled';

    const actions = document.createElement('div');
    actions.className = 'plugin-actions';

    const toggleBtn = document.createElement('button');
    toggleBtn.className = `btn btn-sm ${plugin.enabled ? 'btn-danger' : 'btn-primary'}`;
    toggleBtn.textContent = plugin.enabled ? 'Disable' : 'Enable';
    toggleBtn.addEventListener('click', async () => {
        await togglePlugin(plugin.name, !plugin.enabled);
    });
    actions.appendChild(toggleBtn);

    card.appendChild(title);
    card.appendChild(description);
    card.appendChild(meta);
    card.appendChild(status);
    card.appendChild(actions);

    return card;
}

async function togglePlugin(name, enable) {
    if (!name) {
        return;
    }

    const action = enable ? 'enable' : 'disable';
    try {
        await apiCall(`/plugins/${encodeURIComponent(name)}/${action}`, 'POST');
        showToast(`Plugin ${enable ? 'enabled' : 'disabled'}: ${name}`, 'success');
        await loadPlugins();
    } catch (error) {
        showToast(`Failed to ${action} plugin ${name}: ${error.message}`, 'error');
    }
}

async function loadLogs() {
    const logsContent = document.getElementById('logs-content');
    if (!logsContent) {
        return;
    }

    try {
        const linesInput = document.getElementById('log-lines');
        const lines = Number.parseInt(linesInput?.value || '100', 10);
        const safeLines = Number.isFinite(lines) && lines > 0 ? lines : 100;
        const data = await apiCall(`/logs?lines=${safeLines}`);

        if (Array.isArray(data.logs) && data.logs.length > 0) {
            logsContent.textContent = data.logs.join('\n');
            return;
        }

        logsContent.textContent = 'No logs available.';
    } catch (error) {
        logsContent.textContent = 'Failed to load logs.';
        showToast(`Failed to load logs: ${error.message}`, 'error');
    }
}

document.getElementById('discord-config-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();

    const replyValue = Number.parseFloat(document.getElementById('reply-percentage').value);
    const cooldownValue = Number.parseInt(document.getElementById('cooldown').value, 10);

    const data = {
        bot_name: document.getElementById('bot-name').value,
        reply_percentage: Number.isFinite(replyValue) ? replyValue / 100 : 0,
        cooldown_seconds: Number.isFinite(cooldownValue) ? cooldownValue : 0,
    };

    try {
        await apiCall('/config/discord', 'PUT', data);
        showToast('Discord settings saved.', 'success');
        await loadConfig();
    } catch (error) {
        showToast(`Failed to save Discord settings: ${error.message}`, 'error');
    }
});

document.getElementById('ai-config-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();

    const tokensValue = Number.parseInt(document.getElementById('max-tokens').value, 10);
    const tempValue = Number.parseFloat(document.getElementById('temperature').value);

    const data = {
        model: document.getElementById('model').value,
        vision_model: document.getElementById('vision-model').value,
        max_tokens: Number.isFinite(tokensValue) ? tokensValue : 1024,
        temperature: Number.isFinite(tempValue) ? tempValue : 0.8,
        system_prompt: document.getElementById('system-prompt').value,
    };

    try {
        await apiCall('/config/ai', 'PUT', data);
        showToast('AI settings saved.', 'success');
        await loadConfig();
    } catch (error) {
        showToast(`Failed to save AI settings: ${error.message}`, 'error');
    }
});

async function addToBlacklist(type) {
    const inputId = type === 'user' ? 'new-blacklist-user' : 'new-blacklist-channel';
    const id = document.getElementById(inputId).value.trim();
    if (!id) {
        return;
    }

    try {
        await apiCall('/blacklist', 'POST', { type, id });
        showToast(`Added to ${type} blacklist.`, 'success');
        document.getElementById(inputId).value = '';
        await loadBlacklist();
    } catch (error) {
        showToast(`Failed to add to ${type} blacklist: ${error.message}`, 'error');
    }
}

async function removeFromBlacklist(type, id) {
    try {
        await apiCall(`/blacklist/${encodeURIComponent(type)}/${encodeURIComponent(id)}`, 'DELETE');
        showToast('Removed from blacklist.', 'success');
        await loadBlacklist();
    } catch (error) {
        showToast(`Failed to remove from blacklist: ${error.message}`, 'error');
    }
}

async function loadBlacklist() {
    try {
        const data = await apiCall('/blacklist');

        renderIDList(
            document.getElementById('user-blacklist-items'),
            data.users || [],
            (id) => removeFromBlacklist('user', id),
        );

        renderIDList(
            document.getElementById('channel-blacklist-items'),
            data.channels || [],
            (id) => removeFromBlacklist('channel', id),
        );
    } catch (error) {
        showToast(`Failed to load blacklist: ${error.message}`, 'error');
    }
}

async function addToWhitelist() {
    const id = document.getElementById('new-whitelist-channel').value.trim();
    if (!id) {
        return;
    }

    try {
        await apiCall('/whitelist', 'POST', { type: 'channel', id });
        showToast('Added to whitelist.', 'success');
        document.getElementById('new-whitelist-channel').value = '';
        await loadWhitelist();
    } catch (error) {
        showToast(`Failed to add to whitelist: ${error.message}`, 'error');
    }
}

async function removeFromWhitelist(id) {
    try {
        await apiCall(`/whitelist/channel/${encodeURIComponent(id)}`, 'DELETE');
        showToast('Removed from whitelist.', 'success');
        await loadWhitelist();
    } catch (error) {
        showToast(`Failed to remove from whitelist: ${error.message}`, 'error');
    }
}

async function loadWhitelist() {
    try {
        const data = await apiCall('/whitelist');
        renderIDList(
            document.getElementById('whitelist-items'),
            data.channels || [],
            (id) => removeFromWhitelist(id),
        );
    } catch (error) {
        showToast(`Failed to load whitelist: ${error.message}`, 'error');
    }
}

function renderIDList(listNode, ids, removeHandler) {
    if (!listNode) {
        return;
    }

    listNode.innerHTML = '';

    ids.forEach((id) => {
        const listItem = document.createElement('li');

        const text = document.createElement('span');
        text.textContent = id;

        const removeBtn = document.createElement('button');
        removeBtn.className = 'btn btn-sm btn-danger';
        removeBtn.textContent = 'Remove';
        removeBtn.addEventListener('click', () => {
            removeHandler(id);
        });

        listItem.appendChild(text);
        listItem.appendChild(removeBtn);
        listNode.appendChild(listItem);
    });
}

async function loadHealthTag() {
    const tagNode = document.getElementById('build-tag');
    if (!tagNode) {
        return;
    }

    try {
        const response = await fetch('/health');
        if (!response.ok) {
            throw new Error(`http ${response.status}`);
        }

        const payload = await response.json();
        const timestamp = Number(payload.timestamp);
        if (!Number.isFinite(timestamp) || timestamp <= 0) {
            throw new Error('invalid health timestamp');
        }

        const dateTag = formatDateTag(timestamp);
        const shortHash = timestamp.toString(16).slice(-6);
        tagNode.textContent = `${dateTag}-${shortHash}`;
    } catch (error) {
        const now = Math.floor(Date.now() / 1000);
        const dateTag = formatDateTag(now);
        const shortHash = now.toString(16).slice(-6);
        tagNode.textContent = `${dateTag}-${shortHash}`;
    }
}

function formatDateTag(unixSeconds) {
    const date = new Date(unixSeconds * 1000);
    const yyyy = date.getUTCFullYear();
    const mm = String(date.getUTCMonth() + 1).padStart(2, '0');
    const dd = String(date.getUTCDate()).padStart(2, '0');
    return `${yyyy}${mm}${dd}`;
}

function showToast(message, type = 'info') {
    let container = document.querySelector('.toast-container');
    if (!container) {
        container = document.createElement('div');
        container.className = 'toast-container';
        document.body.appendChild(container);
    }

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.textContent = message;
    container.appendChild(toast);

    setTimeout(() => {
        toast.remove();
    }, 3200);
}
