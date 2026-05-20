(function initEntryCodeHelpers() {
  let entryCodeLength = 4;

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

  window.ChatEntryCode = {
    setEntryCodeLength,
    getEntryCodeLength,
    normalizeEntryCode,
    isValidEntryCode,
    parseEntryCode,
  };
})();
