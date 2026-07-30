(function () {
  let savedMode = "light";
  try {
    savedMode = localStorage.getItem("attendanceQuestAdminTheme");
  } catch {
    savedMode = "light";
  }
  document.documentElement.dataset.adminMode = savedMode === "dark" ? "dark" : "light";
})();
