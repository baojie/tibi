// Copies text for the service worker (which has no clipboard access).
chrome.runtime.onMessage.addListener((m, _sender, reply) => {
  if (m.target !== "offscreen" || m.type !== "copy") return;
  const t = document.getElementById("t");
  t.value = m.text;
  t.select();
  reply(document.execCommand("copy"));
});
