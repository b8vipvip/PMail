<template>
  <div class="settings-card">
    <div class="settings-header">
      <h3>外发邮件</h3>
      <p class="settings-desc">选择 Direct MX、经过认证的 SMTP Relay，或腾讯云 SES API。所有密码和 SecretKey 只写入服务器，不会回显到浏览器。</p>
    </div>

    <el-alert
      v-if="form.mode === 'direct'"
      title="Direct MX 需要服务器能够主动连接互联网 TCP 25。云厂商限制出方向 25 时，请改用 SMTP Relay 或腾讯 SES API。"
      type="warning"
      :closable="false"
      show-icon
      class="settings-alert"
    />

    <el-alert
      v-if="form.mode === 'tencent_ses'"
      title="腾讯 SES 的 SendEmail API 对新账号默认要求使用审核通过的模板；PMail 自由编辑邮件使用 Simple 内容，仅适用于已获得该特殊权限的 SES 账号。若返回 FailedOperation.WithOutPermission，请使用 SMTP Relay，或为验证码业务单独接入 SES 模板 API。"
      type="warning"
      :closable="false"
      show-icon
      class="settings-alert"
    />

    <el-form label-position="top" class="settings-form">
      <el-form-item label="外发模式">
        <el-radio-group v-model="form.mode">
          <el-radio-button label="direct">Direct MX</el-radio-button>
          <el-radio-button label="relay">SMTP Relay</el-radio-button>
          <el-radio-button label="tencent_ses">腾讯 SES API</el-radio-button>
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

      <template v-if="form.mode === 'tencent_ses'">
        <div class="two-cols ses-cols">
          <el-form-item label="SES 地域">
            <el-select v-model="form.tencent_region">
              <el-option label="广州 ap-guangzhou" value="ap-guangzhou" />
              <el-option label="中国香港 ap-hongkong" value="ap-hongkong" />
            </el-select>
          </el-form-item>
          <el-form-item label="邮件类型">
            <el-select v-model="form.tencent_trigger_type">
              <el-option label="普通邮件（0）" :value="0" />
              <el-option label="触发邮件 / 验证码（1）" :value="1" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item label="SecretId">
          <el-input v-model.trim="form.tencent_secret_id" autocomplete="off" placeholder="腾讯云 API SecretId" />
        </el-form-item>

        <el-form-item label="SecretKey">
          <el-input
            v-model="form.tencent_secret_key"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="form.tencent_secret_key_set ? '已保存 SecretKey；留空表示保持不变' : '腾讯云 API SecretKey'"
          />
          <div class="secret-hint" v-if="form.tencent_secret_key_set">服务器已有 SecretKey。页面不会读取或显示原值。</div>
        </el-form-item>

        <el-form-item label="SES 发信地址（FromEmailAddress）">
          <el-input v-model.trim="form.tencent_from_address" placeholder="例如 pmail@send.example.com" />
          <div class="secret-hint">必须是在腾讯 SES 控制台创建并可用的发信地址。PMail 会把原账号地址作为 Header-From / Reply-To，便于对方直接回复到你的 PMail 邮箱。</div>
        </el-form-item>

        <el-alert
          title="建议为 SES 使用独立子域名（例如 send.example.com），不要修改当前正常收信域名的 MX。API 调用固定使用腾讯云官方 ses.tencentcloudapi.com，并通过 TC3-HMAC-SHA256 签名。"
          type="info"
          :closable="false"
          show-icon
          class="settings-alert inner-alert"
        />
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
  security: 'starttls',
  tencent_secret_id: '',
  tencent_secret_key: '',
  tencent_secret_key_set: false,
  tencent_region: 'ap-guangzhou',
  tencent_from_address: '',
  tencent_trigger_type: 0
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
    form.tencent_secret_key = ''
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
  if (form.mode === 'tencent_ses') {
    if (!form.tencent_secret_id) {
      ElMessage.warning('请填写腾讯云 SecretId')
      return
    }
    if (!form.tencent_secret_key_set && !form.tencent_secret_key) {
      ElMessage.warning('请填写腾讯云 SecretKey')
      return
    }
    if (!form.tencent_from_address) {
      ElMessage.warning('请填写腾讯 SES 发信地址')
      return
    }
  }

  saving.value = true
  try {
    const res = await http.post('/api/settings/outbound', {
      mode: form.mode,
      host: form.host,
      port: form.port,
      username: form.username,
      password: form.password,
      security: form.security,
      tencent_secret_id: form.tencent_secret_id,
      tencent_secret_key: form.tencent_secret_key,
      tencent_region: form.tencent_region,
      tencent_from_address: form.tencent_from_address,
      tencent_trigger_type: form.tencent_trigger_type
    })
    if (res.errorNo !== 0) {
      ElMessage.error(res.data || res.errorMsg || '保存失败')
      return
    }
    Object.assign(form, res.data || {})
    form.password = ''
    form.tencent_secret_key = ''
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
.inner-alert { margin-top: 4px; }
.settings-form { width: 100%; }
.two-cols { display: grid; grid-template-columns: 160px 1fr; gap: 16px; }
.ses-cols { grid-template-columns: 1fr 1fr; }
.secret-hint { margin-top: 6px; color: var(--pm-text-secondary); font-size: 12px; line-height: 1.5; }
.form-actions { margin-top: 24px; }
@media (max-width: 768px) { .two-cols, .ses-cols { grid-template-columns: 1fr; gap: 0; } }
</style>
