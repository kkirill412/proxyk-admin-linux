export type Session = { username: string; csrf_token: string };
export type Connection = { link: string; qr_data_url: string };

export type VPNUser = {
  id: number;
  name: string;
  enabled: boolean;
  status: "active" | "blocked";
  pending: boolean;
  delete_pending: boolean;
  blocked_reason: string;
  traffic_limit_bytes: number | null;
  total_bytes: number;
  uplink_bytes: number;
  downlink_bytes: number;
  remaining_bytes: number | null;
  created_at: string;
};

type RequestOptions = { method?: string; body?: object; csrf?: string };

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public id?: number,
  ) {
    super(message);
  }
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers({ Accept: "application/json" });
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (options.csrf) headers.set("X-CSRF-Token", options.csrf);
  const response = await fetch(path, {
    method: options.method ?? "GET",
    headers,
    body: JSON.stringify(options.body),
    credentials: "same-origin",
    cache: "no-store",
    signal: AbortSignal.timeout(20000),
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new ApiError(
      response.status,
      typeof payload?.error === "string" ? payload.error : "Запрос не выполнен.",
      typeof payload?.id === "number" ? payload.id : undefined,
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json();
}

export function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return "Не удалось связаться с панелью. Проверьте соединение и повторите попытку.";
}
