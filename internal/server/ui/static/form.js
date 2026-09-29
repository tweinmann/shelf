// Turns the artifact and domain fields of a form into choices: the deploy artifacts and tags the
// chosen registry connection can pull, and the zones of the chosen Cloudflare connection. The
// text field stays what the form sends; a choice only fills it in. Without JavaScript, without a
// connection, or when a list cannot be read, the text field is simply shown, as it always was.
(function () {
  const OTHER = "\u0000other";

  function option(value, text) {
    const o = document.createElement("option");
    o.value = value;
    o.textContent = text;
    return o;
  }

  function fill(select, options, wanted) {
    select.replaceChildren(...options);
    select.value = wanted;
    if (select.value !== wanted) select.selectedIndex = 0;
  }

  // list fetches the choices behind an api route: {items} or, when they cannot be read,
  // {error}. A reply that is not JSON, such as the login page after the session ran out, counts
  // as an error too.
  async function list(url) {
    try {
      const resp = await fetch(url, { headers: { Accept: "application/json" } });
      const body = await resp.json();
      return resp.ok ? { items: body.items || [] } : { error: body.error || "HTTP " + resp.status };
    } catch (e) {
      return { error: "the list could not be read" };
    }
  }

  function day(stamp) {
    return stamp && !stamp.startsWith("0001") ? stamp.slice(0, 10) : "";
  }

  // latest makes sure only the answer to the newest request is used when the choice changes
  // faster than the lists arrive.
  function latest() {
    let n = 0;
    return function (promise) {
      const mine = ++n;
      return promise.then((v) => (mine === n ? v : null));
    };
  }

  // splitArtifact cuts oci://host/owner/name:tag into the artifact and the tag.
  function splitArtifact(value) {
    const colon = value.lastIndexOf(":");
    if (colon > value.lastIndexOf("/") && colon > "oci:".length) {
      return { base: value.slice(0, colon), tag: value.slice(colon + 1) };
    }
    return { base: value, tag: "" };
  }

  function artifactPicker(input) {
    const source = document.getElementById(input.dataset.registryFrom || "");
    const tagOnly = "tagOnly" in input.dataset;
    const label = document.querySelector('label[for="' + input.id + '"]');
    const box = document.createElement("div");
    box.className = "picker";
    box.hidden = true;
    const pkg = document.createElement("select");
    pkg.id = input.id + "-package";
    pkg.setAttribute("aria-label", "Package");
    const tag = document.createElement("select");
    tag.id = input.id + "-tag";
    tag.setAttribute("aria-label", "Tag");
    if (!tagOnly) box.append(pkg);
    box.append(tag);
    const hint = document.createElement("p");
    hint.className = "muted";
    hint.hidden = true;
    input.before(box);
    input.after(hint);
    const packages = latest();
    const tags = latest();
    let wanted = splitArtifact(input.value);

    function registry() {
      return source ? source.value : input.dataset.registry || "";
    }

    function manual(reason) {
      box.hidden = true;
      input.hidden = false;
      hint.textContent = reason ? "Enter the artifact: " + reason : "";
      hint.hidden = !reason;
      if (label) label.htmlFor = input.id;
    }

    function chosen(select) {
      box.hidden = false;
      hint.hidden = true;
      if (label) label.htmlFor = select.id;
    }

    function compose() {
      const base = tagOnly ? wanted.base : pkg.value;
      if (!tagOnly && base === OTHER) {
        tag.hidden = true;
        input.hidden = false;
        return;
      }
      tag.hidden = false;
      if (tag.value === OTHER) {
        input.hidden = false;
        if (!input.value.startsWith(base + ":")) input.value = base + ":";
        return;
      }
      input.hidden = true;
      input.value = base + ":" + tag.value;
      wanted = splitArtifact(input.value);
    }

    async function loadTags(base) {
      const url =
        "/api/connections/registry/" + encodeURIComponent(registry()) +
        "/tags?artifact=" + encodeURIComponent(base);
      const reply = await tags(list(url));
      if (!reply) return;
      if (reply.error) {
        if (tagOnly) return manual(reply.error);
        fill(tag, [option(OTHER, "other tag…")], OTHER);
        hint.textContent = "The tags could not be read: " + reply.error;
        hint.hidden = false;
        return compose();
      }
      const options = reply.items.map((t) => {
        const when = day(t.created);
        return option(t.name, when ? t.name + " (" + when + ")" : t.name);
      });
      options.push(option(OTHER, "other tag…"));
      hint.hidden = true;
      const current = base === wanted.base ? wanted.tag : "";
      fill(tag, options, current || (reply.items.length ? reply.items[0].name : OTHER));
      if (current && tag.value !== current) {
        // A tag the list does not know, such as one pushed a moment ago: keep what is there.
        tag.value = OTHER;
      }
      if (tagOnly) chosen(tag);
      compose();
    }

    async function loadPackages() {
      const name = registry();
      if (!name) {
        // Drop the answers still on their way for the connection that was chosen before.
        packages(Promise.resolve(null));
        tags(Promise.resolve(null));
        return manual("");
      }
      if (tagOnly) return loadTags(wanted.base);
      const reply = await packages(list("/api/connections/registry/" + encodeURIComponent(name) + "/packages"));
      if (!reply) return;
      if (reply.error) return manual(reply.error);
      const options = reply.items.map((p) => option(p.artifact, p.name));
      options.push(option(OTHER, "other artifact…"));
      const pick = wanted.base || (reply.items.length ? reply.items[0].artifact : OTHER);
      fill(pkg, options, pick);
      if (pkg.value !== pick) pkg.value = OTHER;
      chosen(pkg);
      await onPackage();
    }

    async function onPackage() {
      if (pkg.value === OTHER) {
        tags(Promise.resolve(null));
        return compose();
      }
      tag.replaceChildren();
      await loadTags(pkg.value);
    }

    pkg.addEventListener("change", onPackage);
    tag.addEventListener("change", compose);
    input.addEventListener("input", () => {
      wanted = splitArtifact(input.value);
    });
    if (source) source.addEventListener("change", loadPackages);
    loadPackages();
  }

  function domainPicker(input) {
    const source = document.getElementById(input.dataset.cloudflareFrom || "");
    if (!source) return;
    const label = document.querySelector('label[for="' + input.id + '"]');
    const select = document.createElement("select");
    select.id = input.id + "-zone";
    select.hidden = true;
    const hint = document.createElement("p");
    hint.className = "muted";
    hint.hidden = true;
    input.before(select);
    input.after(hint);
    const zones = latest();

    function manual(reason) {
      select.hidden = true;
      input.hidden = false;
      hint.textContent = reason ? "Enter the domain: " + reason : "";
      hint.hidden = !reason;
      if (label) label.htmlFor = input.id;
    }

    function apply() {
      if (select.value === OTHER) {
        input.hidden = false;
        return;
      }
      input.hidden = true;
      input.value = select.value;
    }

    async function load() {
      // An empty choice or one starting with "@" (a quick tunnel, or none) is no connection.
      if (!source.value || source.value.startsWith("@")) {
        zones(Promise.resolve(null));
        return manual("");
      }
      const reply = await zones(list("/api/connections/cloudflare/" + encodeURIComponent(source.value) + "/zones"));
      if (!reply) return;
      if (reply.error) return manual(reply.error);
      const options = [option("", input.dataset.emptyLabel || "the cluster's domain")];
      reply.items.forEach((z) => options.push(option(z, z)));
      options.push(option(OTHER, "other domain…"));
      const current = input.value.trim();
      fill(select, options, current);
      if (select.value !== current) select.value = OTHER;
      select.hidden = false;
      hint.hidden = true;
      if (label) label.htmlFor = select.id;
      apply();
    }

    select.addEventListener("change", apply);
    source.addEventListener("change", load);
    load();
  }

  document.querySelectorAll("input[data-registry-from], input[data-registry]").forEach(artifactPicker);
  document.querySelectorAll("input[data-cloudflare-from]").forEach(domainPicker);
})();
