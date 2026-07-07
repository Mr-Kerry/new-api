import React, { useEffect, useState } from 'react';
import { Button, Empty, Spin, Banner } from '@douyinfe/semi-ui';
import { IconExternalOpen, IconShoppingCart } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError } from '../../helpers';

export default function TopStore() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [topupLink, setTopupLink] = useState('');

  const loadTopupInfo = async () => {
    setLoading(true);

    try {
      const res = await API.get('/api/user/topup/info');
      const { success, message, data } = res.data;

      if (!success) {
        showError(message || t('获取充值配置失败'));
        setTopupLink('');
        return;
      }

      setTopupLink((data?.topup_link || '').trim());
    } catch (error) {
      showError(t('获取充值配置异常'));
      setTopupLink('');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadTopupInfo();
  }, []);

  if (loading) {
    return (
      <div
        style={{
          height: 'calc(100vh - 64px)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Spin size='large' />
      </div>
    );
  }

  if (!topupLink) {
    return (
      <div style={{ padding: 24 }}>
        <Banner
          type='warning'
          description={t('Top-up store link is not configured.')}
        />
        <div style={{ marginTop: 24 }}>
          <Empty description={t('暂无充值店铺链接')} />
        </div>
      </div>
    );
  }

  return (
    <div
      style={{
        height: 'calc(100vh - 64px)',
        display: 'flex',
        flexDirection: 'column',
        gap: 12,
        padding: 12,
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            fontSize: 14,
            fontWeight: 600,
          }}
        >
          <IconShoppingCart />
          {t('Top-up Store')}
        </div>

        <Button
          theme='borderless'
          icon={<IconExternalOpen />}
          onClick={() => window.open(topupLink, '_blank', 'noopener,noreferrer')}
        >
          {t('Open in new tab')}
        </Button>
      </div>

      <div
        style={{
          minHeight: 0,
          flex: 1,
          overflow: 'hidden',
          border: '1px solid var(--semi-color-border)',
          borderRadius: 8,
          background: 'var(--semi-color-bg-0)',
        }}
      >
        <iframe
          src={topupLink}
          title='Top-up Store'
          style={{
            width: '100%',
            height: '100%',
            border: 0,
          }}
          allow='clipboard-read; clipboard-write; payment'
          referrerPolicy='strict-origin-when-cross-origin'
        />
      </div>
    </div>
  );
}