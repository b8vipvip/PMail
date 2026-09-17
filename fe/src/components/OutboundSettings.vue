<template>
  <div class="settings-card">
    <div class="settings-header">
      <h3>外发邮件</h3>
      <p class="settings-desc">选择直接投递到收件方 MX，或通过经过认证的 SMTP Relay 外发。Relay 密码只写入服务器，不会回显到浏览器。</p>
    </div>

    <el-alert
      v-if="form.mode === 'direct'"
      title="Direct MX 需要服务器能够主动连接互联网 TCP 25。云厂商限制出方向 25 时，请使用 SMTP Relay。"
      type="warning"
      :closable="false"
      show-icon
      class="settings-alert"
    />

    <el-form label-position="top" class="settings-form">
      <el-form-item label="外发模式">
        <el-radio-group v-model="form.mode">
          <el-radio-button value="direct">Direct MX</el-radio-button>
          <el-radio-button value="relay">SMTP Relay</el-radio-button>
        </el-radio-group>
      </el-form-item>

      <template v-if="form.mode === 'relay'">
        <el-form-item label="SMTP Relay Host">
          <el-input v-model.trim="form.host" placeholder="smtp.example.com" />
        </el-form-item>

        <div class="two-cols">
          <el-form-item label="端口">
            <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" />
          </el-form-item>
          <el-form-item label="连接安全">
            <el-select v-model="form.security" @change="securityChanged">
              <el-option label="STARTTLS（通常 587）" value="starttls" />
              <el-option label="TLS / SMTPS（通常 465）" value="tls" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="SMTP 用户名">
          <el-input v-model.trim="form.username" autocomplete="off" placeholder="SMTP username" />
        </el-form-item>

        <el-form-item label="SMTP 密码 / 授权码">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="form.password_set ? '已保存密码；留空表示保持不变' : '请输入 SMTP 密码或授权码'"
          />
          <div class="secret-hint" v-if="form.password_set">服务器已有密码。为了安全，页面不会读取或显示原密码。</div>
        </el-form-item>
      </template>

      <div class="form-actions">
        <el-button type="primary" :loading="saving" @click="save">保存外发设置</el-button>
      </div>
    </el-form>
  </div>
</template>

<script setup>
import {onMounted, reactive, ref} from 'vue'
import {ElMessage} from 'element-plus'
import {http} from '@/utils/axios'

const saving = ref(false)
const form = reactive({
  mode: 'direct',
  host: '',
  port: 587,
  username: '',
  password: '',
  password_set: false,
  security: 'starttls'
})

const load = async () => {
  try {
    const res = await http.get('/api/settings/outbound')
    if (res.errorNo !== 0) {
      ElMessage.error(res.data || res.errorMsg || '读取外发设置失败')
      return
    }
    Object.assign(form, res.data || {})
    form.password = ''
  } catch (e) {
    ElMessage.error('读取外发设置失败')
  }
}

const securityChanged = (value) => {
  if (value === 'tls' && (form.port === 0 || form.port === 587)) form.port = 465
  if (value === 'starttls' && (form.port === 0 || form.port === 465)) form.port = 587
}

const save = async () => {
  if (form.mode === 'relay' && !form.host) {
    ElMessage.warning('请填写 SMTP Relay Host')
    return
  }
  saving.value = true
  try {
    const res = await http.post('/api/settings/outbound', {
      mode: form.mode,
      host: form.host,
      port: form.port,
      username: form.username,
      password: form.password,
      security: form.security
    })
    if (res.errorNo !== 0) {
      ElMessage.error(res.data || res.errorMsg || '保存失败')
      return
    }
    Object.assign(form, res.data || {})
    form.password = ''
    ElMessage.success('外发设置已保存，新邮件立即生效，无需重启 PMail')
  } catch (e) {
    ElMessage.error('保存外发设置失败')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.settings-card { padding: 0; }
.settings-header { margin-bottom: 20px; }
.settings-header h3 { margin: 0 0 8px; font-size: 18px; font-weight: 600; color: var(--pm-text-primary); }
.settings-desc { margin: 0; color: var(--pm-text-secondary); font-size: 14px; line-height: 1.6; }
.settings-alert { margin-bottom: 20px; }
.settings-form { width: 100%; }
.two-cols { display: grid; grid-template-columns: 160px 1fr; gap: 16px; }
.secret-hint { margin-top: 6px; color: var(--pm-text-secondary); font-size: 12px; }
.form-actions { margin-top: 24px; }
@media (max-width: 768px) { .two-cols { grid-template-columns: 1fr; gap: 0; } }
</style>
