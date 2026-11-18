// Champion and item icons come from Riot's Data Dragon. If one fails to
// load (new champion, odd id, offline), drop the image so the initials
// placeholder behind it shows instead of a broken image.
document.addEventListener("error", function (e) {
  var el = e.target;
  if (el && el.tagName === "IMG") {
    if (el.parentElement && el.parentElement.classList.contains("icon")) {
      el.remove();
    } else {
      el.style.visibility = "hidden";
    }
  }
}, true);
