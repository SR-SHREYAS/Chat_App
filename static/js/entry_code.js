(function initEntryCodeHelpers() {
  let entryCodeLength = 4;
  const roomCodeStoragePrefix = "signed-room-code:";

  function setEntryCodeLength(length) {
    const parsed = Number.parseInt(length, 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      entryCodeLength = parsed;
    }
    return entryCodeLength;
  }

  function getEntryCodeLength() {
    return entryCodeLength;
  }

  function normalizeEntryCode(value) {
    return String(value || "").trim();
  }

  function isValidEntryCode(value) {
    const code = normalizeEntryCode(value);
    const pattern = new RegExp(`^\\d{${entryCodeLength}}$`);
    return pattern.test(code);
  }

  function parseEntryCode(value) {
    const code = normalizeEntryCode(value);
    if (!isValidEntryCode(code)) {
      return { error: `Entry code must be exactly ${entryCodeLength} digits` };
    }
    return { value: code };
  }

  function entryCodeStorageKey(roomName) {
    return `${roomCodeStoragePrefix}${String(roomName || "").trim()}`;
  }

  function persistEntryCodeForRoom(roomName, value) {
    const room = String(roomName || "").trim();
    if (!room) {
      return;
    }
    const parsed = parseEntryCode(value);
    if (parsed.error) {
      return;
    }
    try {
      sessionStorage.setItem(entryCodeStorageKey(room), parsed.value);
    } catch (_err) {
      // Ignore storage failures.
    }
  }

  function loadEntryCodeForRoom(roomName) {
    const room = String(roomName || "").trim();
    if (!room) {
      return "";
    }
    try {
      return normalizeEntryCode(sessionStorage.getItem(entryCodeStorageKey(room)));
    } catch (_err) {
      return "";
    }
  }

  window.ChatEntryCode = {
    setEntryCodeLength,
    getEntryCodeLength,
    normalizeEntryCode,
    isValidEntryCode,
    parseEntryCode,
    persistEntryCodeForRoom,
    loadEntryCodeForRoom,
  };
})();
