import { API_BASE_URL } from './config.js'
import { startBackground } from './background-runtime.js'

startBackground(chrome, { baseUrl: API_BASE_URL })
