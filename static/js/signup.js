const signupForm = document.getElementById("signupForm");
const authMessage = document.getElementById("authMessage");

function setMessage(text, isError = false) {
  authMessage.textContent = text || "";
  authMessage.classList.toggle("error", Boolean(isError && text));
}

async function checkSession() {
  try {
    const res = await fetch("/api/auth/me", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) return;
    const payload = await res.json();
    if (payload.authenticated) {
      window.location.href = "/dashboard";
    }
  } catch (err) {
    // Keep user on sign-up page if session check fails.
  }
}

signupForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setMessage("");

  const formData = new FormData(signupForm);
  const body = {
    email: String(formData.get("email") || "").trim(),
    username: String(formData.get("username") || "").trim(),
    password: String(formData.get("password") || ""),
  };

  try {
    const res = await fetch("/api/auth/signup", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify(body),
    });

    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Sign up failed");
    }

    if (payload.authenticated) {
      window.location.href = "/dashboard";
      return;
    }
    throw new Error("Unexpected response");
  } catch (err) {
    setMessage(err.message || "Sign up failed", true);
  }
});

checkSession();

