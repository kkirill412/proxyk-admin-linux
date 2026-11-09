<script setup lang="ts">
import { onMounted, ref } from "vue";
import { ElAlert, ElButton, ElForm, ElFormItem, ElInput } from "element-plus";
import { ApiError, errorText, request, type Session } from "./api";
import Dashboard from "./Dashboard.vue";

const session = ref<Session | null>(null);
const checking = ref(true);
const busy = ref(false);
const error = ref("");
const username = ref("");
const password = ref("");
const theme = localStorage.getItem("theme");
const dark = ref(theme === "dark" || (!theme && matchMedia("(prefers-color-scheme: dark)").matches));

function applyTheme() {
  document.documentElement.classList.toggle("dark", dark.value);
}

function toggleTheme() {
  dark.value = !dark.value;
  localStorage.setItem("theme", dark.value ? "dark" : "light");
  applyTheme();
}

async function restoreSession() {
  try {
    session.value = await request<Session>("/api/auth/session");
  } catch (cause) {
    if (!(cause instanceof ApiError && cause.status === 401)) error.value = errorText(cause);
  } finally {
    checking.value = false;
  }
}

async function login() {
  if (busy.value) return;
  error.value = "";
  if (!username.value.trim() || !password.value) {
    error.value = "Введите логин и пароль.";
    return;
  }
  busy.value = true;
  try {
    session.value = await request<Session>("/api/auth/login", {
      method: "POST",
      body: { username: username.value.trim(), password: password.value },
    });
    password.value = "";
  } catch (cause) {
    error.value = errorText(cause);
  } finally {
    busy.value = false;
  }
}

async function logout() {
  if (!session.value || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await request<void>("/api/auth/logout", { method: "POST", csrf: session.value.csrf_token });
    session.value = null;
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 401) session.value = null;
    else error.value = errorText(cause);
  } finally {
    busy.value = false;
  }
}

function expireSession() {
  if (!session.value) return;
  session.value = null;
  error.value = "Сеанс завершился. Войдите снова.";
}

applyTheme();
onMounted(restoreSession);
</script>

<template>
  <div class="shell">
    <header class="site-header">
      <a href="/" class="brand" aria-label="ProxyK Admin, главная">
        <span class="mark" aria-hidden="true">P<span>↗</span></span>
        proxy<span class="brand-k">k</span><span class="brand-admin">ADMIN</span>
      </a>
      <div class="header-actions">
        <ElButton text @click="toggleTheme">{{ dark ? "Светлая тема" : "Тёмная тема" }}</ElButton>
        <ElButton v-if="session" :loading="busy" @click="logout">Выйти</ElButton>
      </div>
    </header>
    <main v-if="checking" class="login-main" aria-live="polite">
      <p class="muted">Проверяем сеанс…</p>
    </main>
    <main v-else-if="session" class="dashboard-main">
      <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <Dashboard :session="session" @expired="expireSession" />
    </main>
    <main v-else class="login-main">
      <section class="login-card" aria-labelledby="login-title">
        <div class="eyebrow"><span></span>ПАНЕЛЬ УПРАВЛЕНИЯ VPN</div>
        <h1 id="login-title">Вход в панель</h1>
        <p class="intro">Введите данные администратора, полученные при установке.</p>
        <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon />
        <ElForm label-position="top" @submit.prevent="login">
          <ElFormItem label="Логин" for="login-username">
            <ElInput id="login-username" v-model="username" autocomplete="username" :disabled="busy" />
          </ElFormItem>
          <ElFormItem label="Пароль" for="login-password">
            <ElInput id="login-password" v-model="password" type="password" autocomplete="current-password" :disabled="busy" />
          </ElFormItem>
          <ElButton type="primary" native-type="submit" :loading="busy" class="login-submit">Войти</ElButton>
        </ElForm>
      </section>
    </main>
    <footer class="site-footer"><span>ProxyK Admin</span><span>Управление доступом к VPN</span></footer>
  </div>
</template>
