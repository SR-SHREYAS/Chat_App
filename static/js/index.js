const authStatus = document.getElementById("authStatus");
const authMessage = document.getElementById("authMessage");
const joinModeNote = document.getElementById("joinModeNote");

const signInForm = document.getElementById("signinForm");
const signUpForm = document.getElementById("signupForm");

const showSigninBtn = document.getElementById("showSigninBtn");
const showSignupBtn = document.getElementById("showSignupBtn");
const signoutBtn = document.getElementById("signoutBtn");

const state = {
  user: null,
  mode: "signin",
};

function setMode(mode) {
  state.mode = mode;
  const signinActive = mode === "signin";

  signInForm.classList.toggle("hidden", !signinActive);
  signUpForm.classList.toggle("hidden", signinActive);

  showSigninBtn.classList.toggle("active", signinActive);
  showSignupBtn.classList.toggle("active", !signinActive);
}

function setMessage(text, isError = false) {
  authMessage.textContent = text || "";
  authMessage.classList.toggle("error", Boolean(isError && text));
}

function setAuthenticatedUser(user) {
  state.user = user || null;

  if (state.user) {
    authStatus.textContent = `Signed in as ${state.user.display_name} (${state.user.email})`;
    joinModeNote.textContent = `Signed-in mode: your account name "${state.user.display_name}" will be used in chat.`;
    signoutBtn.classList.remove("hidden");
    document.getElementById("authTabs").classList.add("hidden");
    signInForm.classList.add("hidden");
    signUpForm.classList.add("hidden");
  } else {
    authStatus.textContent = "Not signed in. You can still join as a guest.";
    joinModeNote.textContent = "Guest mode: you will join with a random name.";
    signoutBtn.classList.add("hidden");
    document.getElementById("authTabs").classList.remove("hidden");
    setMode(state.mode);
  }
}

async function loadSession() {
  try {
    const res = await fetch("/api/auth/me", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      throw new Error("Session fetch failed");
    }
    const payload = await res.json();
    if (payload.authenticated && payload.user) {
      setAuthenticatedUser(payload.user);
    } else {
      setAuthenticatedUser(null);
    }
  } catch (err) {
    setAuthenticatedUser(null);
    setMessage("Could not verify session right now. Guest mode is still available.", true);
  }
}

async function submitAuthForm(url, body, successText) {
  setMessage("");
  try {
    const res = await fetch(url, {
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
      throw new Error(payload.error || "Request failed");
    }

    if (payload.authenticated && payload.user) {
      setAuthenticatedUser(payload.user);
      setMessage(successText);
      signInForm.reset();
      signUpForm.reset();
      return;
    }

    throw new Error("Unexpected authentication response");
  } catch (err) {
    setMessage(err.message || "Authentication failed", true);
  }
}

showSigninBtn.addEventListener("click", () => {
  setMode("signin");
  setMessage("");
});

showSignupBtn.addEventListener("click", () => {
  setMode("signup");
  setMessage("");
});

signInForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const formData = new FormData(signInForm);
  await submitAuthForm(
    "/api/auth/signin",
    {
      email: String(formData.get("email") || ""),
      password: String(formData.get("password") || ""),
    },
    "Signed in successfully."
  );
});

signUpForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const formData = new FormData(signUpForm);
  await submitAuthForm(
    "/api/auth/signup",
    {
      display_name: String(formData.get("display_name") || ""),
      email: String(formData.get("email") || ""),
      password: String(formData.get("password") || ""),
    },
    "Account created and signed in."
  );
});

signoutBtn.addEventListener("click", async () => {
  setMessage("");
  try {
    await fetch("/api/auth/signout", {
      method: "POST",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
  } finally {
    setAuthenticatedUser(null);
    setMessage("Signed out.");
  }
});

setMode("signin");
loadSession();

