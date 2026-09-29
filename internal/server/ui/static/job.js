// Appends what a running change reports. The page is rendered with the log so far; this picks
// up from there, so a reload during a five-minute change loses nothing. Without JavaScript the
// page still shows everything that happened up to the moment it was loaded.
(function () {
  const script = document.currentScript;
  const log = document.getElementById("job-log");
  const state = document.getElementById("job-state");
  if (!script || !log) return;

  const source = new EventSource(
    "/api/jobs/" + encodeURIComponent(script.dataset.job) + "/events?from=" + script.dataset.from
  );

  source.addEventListener("line", function (event) {
    log.textContent += JSON.parse(event.data).text;
    window.scrollTo(0, document.body.scrollHeight);
  });

  source.addEventListener("end", function (event) {
    const error = JSON.parse(event.data).error;
    source.close();
    if (state) {
      state.textContent = error ? "failed" : "done";
      state.className = "pill " + (error ? "Failed" : "Ready");
    }
    if (error) {
      const p = document.createElement("p");
      p.className = "problem-line";
      p.textContent = error;
      log.after(p);
    }
  });
})();
