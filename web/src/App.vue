<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElButton, ElTag, ElAlert } from "element-plus";

type Health = { status: string; database: boolean; xray: boolean };
const health = ref<Health | null>(null);
const error = ref("");
const loading = ref(false);
const dark = ref(
  localStorage.getItem("theme") === "dark" ||
    (!localStorage.getItem("theme") &&
      matchMedia("(prefers-color-scheme: dark)").matches),
);
const ready = computed(() => health.value?.status === "ready");
let timer: ReturnType<typeof setInterval>;

function applyTheme() {
  document.documentElement.classList.toggle("dark", dark.value);
}
function toggleTheme() {
  dark.value = !dark.value;
  localStorage.setItem("theme", dark.value ? "dark" : "light");
  applyTheme();
}
async function refresh() {
  if (loading.value) return;
  loading.value = true;
  try {
    const response = await fetch("/health/ready", {
      cache: "no-store",
      signal: AbortSignal.timeout(6000),
    });
    if (response.status !== 200 && response.status !== 503)
      throw new Error("status");
    const data: Health = await response.json();
    if (typeof data.database !== "boolean" || typeof data.xray !== "boolean")
      throw new Error("response");
    health.value = data;
    error.value = "";
  } catch {
    health.value = null;
    error.value =
      "Не удалось проверить сервер. Проверьте соединение и повторите попытку.";
  } finally {
    loading.value = false;
  }
}
applyTheme();
onMounted(() => {
  void refresh();
  timer = setInterval(() => void refresh(), 15000);
});
onUnmounted(() => clearInterval(timer));
</script>

<template>
  <div class="shell">
    <header>
      <a href="/" class="brand" aria-label="ProxyK, главная"
        ><span class="mark">P<span>↗</span></span
        >proxy<span class="brand-k">k</span
        ><span class="brand-admin">ADMIN</span></a
      >
      <ElButton text @click="toggleTheme">{{
        dark ? "Светлая тема" : "Тёмная тема"
      }}</ElButton>
    </header>
    <main>
      <div class="eyebrow"><span></span>ВАШ ЛИЧНЫЙ СЕРВЕР</div>
      <div class="heading-row">
        <div>
          <h1>Всё начинается<br />с надёжной основы.</h1>
          <p class="intro">Состояние сервисов вашей VPN-панели.</p>
        </div>
        <ElButton :loading="loading" @click="refresh"
          >Проверить соединение ↗</ElButton
        >
      </div>
      <ElAlert
        v-if="error"
        :title="error"
        type="error"
        :closable="false"
        show-icon
      />
      <section
        class="status-grid"
        aria-label="Состояние сервера"
        aria-live="polite"
      >
        <article class="status-card primary">
          <div class="card-top">
            <span>Система</span><span class="card-icon">↗</span>
          </div>
          <div class="status-title">
            {{
              ready
                ? "На связи"
                : health
                  ? "Нужна проверка"
                  : loading
                    ? "Проверяем…"
                    : "Нет связи"
            }}
          </div>
          <p>
            {{
              ready
                ? "Основные сервисы отвечают"
                : "Статус обновляется автоматически"
            }}
          </p>
          <div class="signal">
            <i :class="{ online: ready }"></i
            >{{ ready ? "СОЕДИНЕНИЕ УСТАНОВЛЕНО" : "ОЖИДАНИЕ ГОТОВНОСТИ" }}
          </div>
        </article>
        <article class="status-card">
          <div class="card-top">
            <span>Хранилище данных</span><span class="card-icon">▤</span>
          </div>
          <div class="status-title">
            {{ health?.database ? "Готово" : health ? "Недоступно" : "—" }}
          </div>
          <p>Постоянное хранение настроек</p>
          <ElTag
            :type="health?.database ? 'success' : 'info'"
            effect="plain"
            round
            >{{ health?.database ? "Доступно" : "Нет подтверждения" }}</ElTag
          >
        </article>
        <article class="status-card">
          <div class="card-top">
            <span>VPN-сервис</span><span class="card-icon">⌁</span>
          </div>
          <div class="status-title">
            {{ health?.xray ? "Запущен" : health ? "Недоступен" : "—" }}
          </div>
          <p>Соединение с Xray-core</p>
          <ElTag
            :type="health?.xray ? 'success' : 'info'"
            effect="plain"
            round
            >{{ health?.xray ? "Отвечает" : "Нет подтверждения" }}</ElTag
          >
        </article>
      </section>
      <section class="notice">
        <span class="notice-index">01</span>
        <div>
          <h2>Базовая установка</h2>
          <p>
            Сейчас доступна проверка инфраструктуры. Вход в панель и управление
            пользователями появятся на следующих этапах разработки.
            VPN-подключения пока не выдаются.
          </p>
        </div>
        <span class="stage">ЭТАП 1 / 4</span>
      </section>
    </main>
    <footer>
      <span>ProxyK Admin</span
      ><span>Просто. Приватно. Под вашим контролем.</span>
    </footer>
  </div>
</template>
