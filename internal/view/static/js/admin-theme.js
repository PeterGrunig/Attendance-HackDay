(function () {
  const root = document.documentElement;
  const storageKey = "attendanceQuestAdminTheme";

  function applyMode(mode) {
    root.dataset.adminMode = mode;
    const toggle = document.querySelector("[data-admin-theme-toggle]");
    const label = document.querySelector("[data-admin-theme-label]");
    if (toggle) {
      toggle.setAttribute("aria-pressed", String(mode === "dark"));
    }
    if (label) {
      label.textContent = mode === "dark" ? "Light mode" : "Dark mode";
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    const toggle = document.querySelector("[data-admin-theme-toggle]");
    applyMode(root.dataset.adminMode === "dark" ? "dark" : "light");
    if (!toggle) {
      return;
    }
    toggle.addEventListener("click", () => {
      const nextMode = root.dataset.adminMode === "dark" ? "light" : "dark";
      try {
        localStorage.setItem(storageKey, nextMode);
      } catch {
        // The selected mode still applies for this page when storage is blocked.
      }
      applyMode(nextMode);
    });
  });
})();
