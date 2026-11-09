<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElAlert, ElButton, ElDialog, ElForm, ElFormItem, ElInput, ElTag } from "element-plus";
import { ApiError, errorText, request, type Connection, type Session, type VPNUser } from "./api";

const props = defineProps<{ session: Session }>();
const emit = defineEmits<{ expired: [] }>();
type Dialog =
  | { kind: "create" }
  | { kind: "name" | "limit" | "delete"; user: VPNUser }
  | { kind: "connection"; details: Connection };
type CreatedUser = VPNUser & { connection?: Connection };

const users = ref<VPNUser[]>([]);
const health = ref<"checking" | "ready" | "unavailable">("checking");
const refreshing = ref(false);
const busy = ref(false);
const listError = ref("");
const notice = ref("");
const dialogError = ref("");
const dialog = ref<Dialog | null>(null);
const name = ref("");
const limit = ref("");
const copyMessage = ref("");
const linkInput = ref<HTMLTextAreaElement | null>(null);
let refreshVersion = 0;
let timer: number | undefined;

const gb = 1_000_000_000;
const numberFormat = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 });
const dateFormat = new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium" });
const activeCount = computed(() => users.value.filter((user) => user.status === "active" && !user.pending).length);
const totalTraffic = computed(() => users.value.reduce((total, user) => total + user.total_bytes, 0));
const titles = {
  create: "Создать пользователя",
  name: "Изменить имя",
  limit: "Изменить лимит",
  delete: "Удалить пользователя",
  connection: "Подключение",
};

function formatBytes(bytes: number): string {
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ", "ПБ"];
  let unit = 0;
  while (bytes >= 1000 && unit < units.length - 1) {
    bytes /= 1000;
    unit++;
  }
  return `${numberFormat.format(bytes)} ${units[unit]}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : dateFormat.format(date);
}

function userStatus(user: VPNUser): string {
  if (user.delete_pending) return "Удаляется";
  if (user.pending) return "Ожидает Xray";
  if (user.status === "active") return "Активен";
  return user.blocked_reason === "limit" ? "Лимит исчерпан" : "Заблокирован";
}

function expired(cause: unknown): boolean {
  if (!(cause instanceof ApiError) || cause.status !== 401) return false;
  emit("expired");
  return true;
}

async function refresh() {
  const version = ++refreshVersion;
  refreshing.value = true;
  const [list, status] = await Promise.allSettled([
    request<VPNUser[]>("/api/users"),
    request<{ status: string }>("/health/ready"),
  ]);
  if (version !== refreshVersion) return;
  refreshing.value = false;
  health.value = status.status === "fulfilled" && status.value.status === "ready" ? "ready" : "unavailable";
  if (list.status === "fulfilled") {
    users.value = list.value;
    listError.value = "";
  } else if (!expired(list.reason)) {
    listError.value = errorText(list.reason);
  }
}

function open(next: Dialog) {
  dialogError.value = "";
  copyMessage.value = "";
  dialog.value = next;
  name.value = next.kind === "name" ? next.user.name : "";
  limit.value = "";
  if (next.kind === "limit" && next.user.traffic_limit_bytes !== null) {
    const bytes = next.user.traffic_limit_bytes;
    const fraction = String(bytes % gb).padStart(9, "0").replace(/0+$/, "");
    limit.value = `${Math.floor(bytes / gb)}${fraction ? `.${fraction}` : ""}`;
  }
}

function closeDialog() {
  if (!busy.value) dialog.value = null;
}

function parseLimit(): number | null {
  const value = limit.value.trim().replace(",", ".");
  if (!value) return null;
  if (!/^\d+(\.\d{1,9})?$/.test(value)) throw new Error("Введите неотрицательный лимит в ГБ.");
  const [whole, fraction = ""] = value.split(".");
  const bytes = Number(whole + fraction.padEnd(9, "0"));
  if (!Number.isSafeInteger(bytes)) throw new Error("Укажите меньший лимит трафика.");
  return bytes;
}

function formBody(kind: "create" | "name" | "limit"): object | null {
  const result: { name?: string; traffic_limit_bytes?: number | null } = {};
  if (kind !== "limit") {
    const value = name.value.trim();
    if (!value || Array.from(value).length > 80) {
      dialogError.value = "Введите имя длиной от 1 до 80 символов.";
      return null;
    }
    result.name = value;
  }
  if (kind !== "name") {
    try {
      result.traffic_limit_bytes = parseLimit();
    } catch (cause) {
      dialogError.value = (cause as Error).message;
      return null;
    }
  }
  return result;
}

async function mutate(method: "POST" | "PATCH" | "DELETE", path: string, body?: object) {
  if (busy.value) return;
  busy.value = true;
  dialogError.value = "";
  notice.value = "";
  try {
    const result = await request<CreatedUser | undefined>(path, { method, body, csrf: props.session.csrf_token });
    dialog.value = null;
    if (result?.connection) open({ kind: "connection", details: result.connection });
    await refresh();
  } catch (cause) {
    if (expired(cause)) return;
    if (cause instanceof ApiError && cause.id !== undefined) {
      dialog.value = null;
      notice.value = `${errorText(cause)} Номер пользователя: ${cause.id}.`;
    } else if (method === "POST" && (!(cause instanceof ApiError) || cause.status >= 500)) {
      dialog.value = null;
      notice.value = "Не удалось подтвердить создание. Проверьте список перед повторной попыткой.";
    } else if (dialog.value) {
      dialogError.value = errorText(cause);
    } else {
      notice.value = errorText(cause);
    }
    await refresh();
  } finally {
    busy.value = false;
  }
}

function submitDialog() {
  const current = dialog.value;
  if (!current || current.kind === "connection") return;
  if (current.kind === "delete") {
    void mutate("DELETE", `/api/users/${current.user.id}`);
    return;
  }
  const body = formBody(current.kind);
  if (!body) return;
  if (current.kind === "create") void mutate("POST", "/api/users", body);
  else void mutate("PATCH", `/api/users/${current.user.id}`, body);
}

async function showConnection(user: VPNUser) {
  if (busy.value) return;
  busy.value = true;
  notice.value = "";
  try {
    const details = await request<Connection>(`/api/users/${user.id}/connection`);
    open({ kind: "connection", details });
  } catch (cause) {
    if (!expired(cause)) notice.value = errorText(cause);
  } finally {
    busy.value = false;
  }
}

async function copyLink(details: Connection) {
  try {
    await navigator.clipboard.writeText(details.link);
    copyMessage.value = "Ссылка скопирована.";
  } catch {
    linkInput.value?.focus();
    linkInput.value?.select();
    copyMessage.value = "Ссылка выделена. Скопируйте её вручную.";
  }
}

onMounted(() => {
  void refresh();
  timer = window.setInterval(() => {
    if (!refreshing.value && !busy.value && !document.hidden) void refresh();
  }, 5000);
});
onUnmounted(() => {
  window.clearInterval(timer);
  refreshVersion++;
});
</script>

<template>
  <div class="eyebrow"><span></span>УПРАВЛЕНИЕ ДОСТУПОМ</div>
  <div class="heading-row">
    <div>
      <h1>Пользователи VPN</h1>
      <p class="intro">Создавайте подключения и управляйте расходом трафика.</p>
    </div>
    <ElButton type="primary" :disabled="busy" @click="open({ kind: 'create' })">Создать пользователя</ElButton>
  </div>

  <section class="status-grid" aria-label="Обзор VPN">
    <div class="status-card primary">
      <div class="card-top">VPN-сервер</div>
      <div class="status-title">{{ { checking: "Проверяем…", ready: "Работает", unavailable: "Недоступен" }[health] }}</div>
      <div class="signal"><i :class="{ online: health === 'ready' }"></i>{{ health === "ready" ? "XRAY ГОТОВ" : "СТАТУС XRAY" }}</div>
    </div>
    <div class="status-card">
      <div class="card-top">Доступ разрешён</div>
      <div class="status-title">{{ numberFormat.format(activeCount) }}</div>
      <p>Всего {{ numberFormat.format(users.length) }}</p>
    </div>
    <div class="status-card">
      <div class="card-top">Израсходовано</div>
      <div class="status-title">{{ formatBytes(totalTraffic) }}</div>
      <p>Входящий и исходящий трафик</p>
    </div>
  </section>

  <section class="users-section" aria-labelledby="users-title">
    <div class="users-header">
      <h2 id="users-title">Список пользователей</h2>
      <ElButton text :loading="refreshing" :disabled="busy" @click="refresh">Обновить</ElButton>
    </div>
    <ElAlert v-if="listError" class="users-alert" :title="listError" type="error" :closable="false" show-icon />
    <ElAlert v-if="notice" class="users-alert" :title="notice" type="warning" :closable="false" show-icon />
    <div v-if="users.length" class="users-panel">
      <table class="users-table" aria-labelledby="users-title">
        <thead>
          <tr>
            <th scope="col">Имя</th>
            <th scope="col">Статус</th>
            <th scope="col">Трафик</th>
            <th scope="col">Лимит</th>
            <th scope="col">Создан</th>
            <th scope="col">Действия</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="user in users" :key="user.id">
            <th scope="row" class="user-name">{{ user.name }}</th>
            <td data-label="Статус">
              <ElTag :type="user.pending || user.delete_pending ? 'warning' : user.status === 'active' ? 'success' : 'info'" effect="light">
                {{ userStatus(user) }}
              </ElTag>
            </td>
            <td data-label="Трафик" class="user-traffic">
              <strong>{{ formatBytes(user.total_bytes) }}</strong>
              <small>↑ {{ formatBytes(user.uplink_bytes) }} · ↓ {{ formatBytes(user.downlink_bytes) }}</small>
            </td>
            <td data-label="Лимит" class="user-limit">
              <template v-if="user.traffic_limit_bytes !== null">
                {{ formatBytes(user.traffic_limit_bytes) }}
                <small v-if="user.remaining_bytes !== null">Осталось {{ formatBytes(user.remaining_bytes) }}</small>
              </template>
              <template v-else>Без лимита</template>
            </td>
            <td data-label="Создан"><time :datetime="user.created_at">{{ formatDate(user.created_at) }}</time></td>
            <td class="user-actions-cell">
              <div v-if="!user.delete_pending" class="user-actions">
                <ElButton size="small" :disabled="busy || user.pending || user.status !== 'active'" @click="showConnection(user)">Подключение</ElButton>
                <ElButton size="small" :disabled="busy" @click="open({ kind: 'name', user })">Изменить имя</ElButton>
                <ElButton size="small" :disabled="busy" @click="open({ kind: 'limit', user })">Изменить лимит</ElButton>
                <ElButton size="small" :disabled="busy" @click="mutate('PATCH', `/api/users/${user.id}`, { enabled: !user.enabled })">
                  {{ user.enabled ? "Заблокировать" : "Разблокировать" }}
                </ElButton>
                <ElButton size="small" type="danger" plain :disabled="busy" @click="open({ kind: 'delete', user })">Удалить</ElButton>
              </div>
              <span v-else class="muted">Ожидаем подтверждения</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else-if="!listError" class="empty-users">{{ refreshing ? "Загружаем список…" : "Пользователей пока нет. Создайте первое подключение." }}</p>
  </section>

  <ElDialog
    :model-value="dialog !== null"
    :title="dialog ? titles[dialog.kind] : ''"
    width="min(460px, calc(100% - 32px))"
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    destroy-on-close
    @update:model-value="closeDialog"
  >
    <div v-if="dialog?.kind === 'connection'" class="connection-dialog">
      <img class="connection-qr" :src="dialog.details.qr_data_url" alt="QR-код подключения" width="224" height="224" />
      <label for="connection-link">Ссылка VLESS</label>
      <textarea id="connection-link" ref="linkInput" :value="dialog.details.link" readonly rows="3" spellcheck="false"></textarea>
      <div class="dialog-actions">
        <ElButton type="primary" :disabled="busy" @click="copyLink(dialog.details)">Скопировать ссылку</ElButton>
        <ElButton :disabled="busy" @click="closeDialog">Закрыть</ElButton>
      </div>
      <p v-if="copyMessage" class="muted" role="status">{{ copyMessage }}</p>
    </div>
    <ElForm v-else-if="dialog" label-position="top" @submit.prevent="submitDialog">
      <ElAlert v-if="dialogError" class="users-alert" :title="dialogError" type="error" :closable="false" show-icon />
      <p v-if="dialog.kind === 'delete'" class="delete-copy">Удалить пользователя «{{ dialog.user.name }}»? Его ссылка перестанет работать для новых подключений.</p>
      <ElFormItem v-if="dialog.kind === 'create' || dialog.kind === 'name'" label="Имя" for="user-name">
        <ElInput id="user-name" v-model="name" :disabled="busy" :maxlength="160" />
      </ElFormItem>
      <ElFormItem v-if="dialog.kind === 'create' || dialog.kind === 'limit'" label="Лимит трафика, ГБ" for="user-limit">
        <ElInput id="user-limit" v-model="limit" inputmode="decimal" :disabled="busy" placeholder="Без лимита" />
        <span class="muted">Оставьте поле пустым для доступа без лимита.</span>
      </ElFormItem>
      <div class="dialog-actions">
        <ElButton :disabled="busy" @click="closeDialog">Отмена</ElButton>
        <ElButton :type="dialog.kind === 'delete' ? 'danger' : 'primary'" native-type="submit" :loading="busy">
          {{ dialog.kind === "create" ? "Создать" : dialog.kind === "delete" ? "Удалить" : "Сохранить" }}
        </ElButton>
      </div>
    </ElForm>
  </ElDialog>
</template>
