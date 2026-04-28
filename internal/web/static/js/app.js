/**
 * EZyapper WebUI — Progressive Enhancement
 *
 * Tab switching, toast auto-dismiss, stats auto-refresh, form guarding.
 * All features degrade gracefully when JavaScript is disabled.
 * Vanilla JS only. No dependencies.
 */

(function () {
	'use strict';

	/* Tab Switching */
	function initTabs() {
		var tabs = document.querySelectorAll('.md3-tab');
		if (!tabs.length) return;

		tabs.forEach(function (tab) {
			tab.addEventListener('click', function () {
				var tabName = tab.getAttribute('data-tab');
				if (!tabName) return;

				var container = tab.closest('.md3-tabs');
				if (!container) return;

				container.querySelectorAll('.md3-tab').forEach(function (t) {
					t.classList.remove('md3-tab--active');
					t.setAttribute('aria-selected', 'false');
				});

				tab.classList.add('md3-tab--active');
				tab.setAttribute('aria-selected', 'true');

				var form = container.closest('form');
				if (!form) return;

				form.querySelectorAll('.md3-tab-panel').forEach(function (panel) {
					panel.classList.remove('md3-tab-panel--active');
				});

				var target = form.querySelector('#tab-' + tabName);
				if (target) {
					target.classList.add('md3-tab-panel--active');
				}
			});
		});
	}

	/* Toast Auto-Dismiss */
	function initToasts() {
		var toasts = document.querySelectorAll('.md3-toast');
		if (!toasts.length) return;

		toasts.forEach(function (toast) {
			setTimeout(function () {
				toast.style.transition = 'opacity 0.3s ease';
				toast.style.opacity = '0';

				setTimeout(function () {
					if (toast.parentNode) {
						toast.parentNode.removeChild(toast);
					}
				}, 350);
			}, 3000);
		});
	}

	/* Stats Auto-Refresh (Dashboard) */
	function initStatsRefresh() {
		if (!document.querySelector('.md3-stat-card')) return;

		setInterval(function () {
			location.reload();
		}, 30000);
	}

	/* Form Enhancement — Prevent Double-Submit */
	function initForms() {
		var forms = document.querySelectorAll('form');
		if (!forms.length) return;

		forms.forEach(function (form) {
			if ((form.getAttribute('method') || 'get').toLowerCase() === 'get') return;

			form.addEventListener('submit', function () {
				var btns = form.querySelectorAll('[type="submit"]');
				btns.forEach(function (btn) {
					btn.disabled = true;
					btn.textContent = 'Saving\u2026';
				});
			});
		});
	}

	document.addEventListener('DOMContentLoaded', function () {
		initTabs();
		initToasts();
		initStatsRefresh();
		initForms();
	});
})();
