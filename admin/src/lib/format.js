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

export const errorMessages = {
  unauthorized: 'Токен неверный или истёк',
  bad_request: 'Некорректные данные',
  not_found: 'Запись не найдена',
  name_taken: 'План с таким названием уже есть',
  protected: 'Базовый план (free/trial) нельзя переименовать, отключить или удалить',
  in_use: 'План используется продуктами или подписками — сначала отвяжите их',
  internal: 'Внутренняя ошибка сервера'
}

export function humanError(err) {
  return errorMessages[err?.code] || err?.message || 'Неизвестная ошибка'
}
