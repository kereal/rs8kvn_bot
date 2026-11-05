const GIB = 1024 ** 3

export function bytesToGib(bytes) {
  if (!bytes) return 0
  return Math.round((bytes / GIB) * 100) / 100
}

export function gibToBytes(gib) {
  const value = Number(gib)
  if (!Number.isFinite(value) || value <= 0) return 0
  return Math.round(value * GIB)
}

export function formatGib(bytes) {
  if (!bytes || bytes <= 0) return '∞'
  return `${bytesToGib(bytes)} ГБ`
}

export function formatDate(value) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date)
}

// Money is stored in cents; the API never sends formatted strings.
export function formatMoney(cents, currency = 'RUB') {
  if (cents === null || cents === undefined) return '—'
  const value = Number(cents) / 100
  if (!Number.isFinite(value)) return '—'
  return new Intl.NumberFormat('ru-RU', {
    style: 'currency',
    currency: currency || 'RUB',
    maximumFractionDigits: value % 1 === 0 ? 0 : 2
  }).format(value)
}

// Durations are stored in days. Russian plural forms: 1 день, 2 дня, 5 дней.
export function formatDays(days) {
  const n = Math.abs(Number(days) || 0)
  const mod10 = n % 10
  const mod100 = n % 100
  let word = 'дней'
  if (mod10 === 1 && mod100 !== 11) word = 'день'
  else if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) word = 'дня'
  return `${n} ${word}`
}

export const errorMessages = {
  unauthorized: 'Токен неверный или истёк',
  bad_request: 'Некорректные данные',
  not_found: 'Запись не найдена',
  name_taken: 'Такая запись уже существует',
  protected: 'Эту запись нельзя изменить или удалить',
  in_use: 'Запись используется — сначала уберите зависимости',
  stale: 'Состояние уже изменилось — обновите список',
  internal: 'Внутренняя ошибка сервера',
  network: 'Бот недоступен — проверьте, что он запущен и адрес ADMIN_LISTEN верный'
}

export function humanError(err) {
  return errorMessages[err?.code] || err?.message || 'Неизвестная ошибка'
}
