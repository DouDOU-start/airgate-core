import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert, Button, Card, Chip, EmptyState, Input, Modal, Spinner, useOverlayState,
} from '@heroui/react';
import QRCode from 'qrcode';
import {
  AlertTriangle, CheckCircle, Clock, ExternalLink, QrCode, Ticket, Wallet,
} from 'lucide-react';
import { paymentApi } from '../../shared/api/payment';
import { redemptionApi } from '../../shared/api/redemption';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useToast } from '../../shared/ui';
import { DEFAULT_PAGE_SIZE } from '../../shared/constants';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { formatDateTime } from '../../shared/utils/format';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import { useSiteSettings } from '../../app/providers/SiteSettingsProvider';
import type { CreatePaymentOrderReq, PaymentOrder, PaymentOrderStatus, RedeemResp } from '../../shared/types';
import { RefreshButton } from '../../shared/components/RefreshButton';

// 金额预设（单位 CNY，1 CNY = $1.00 额度）
const PRESET_AMOUNTS = [10, 30, 50, 100, 200, 500];

// 订单状态徽章配色
const STATUS_CHIP_COLORS: Record<PaymentOrderStatus, 'warning' | 'success' | 'default'> = {
  pending: 'warning',
  paid: 'success',
  expired: 'default',
};

function PaymentStatusChip({ status }: { status: PaymentOrderStatus }) {
  const { t } = useTranslation();
  return (
    <Chip color={STATUS_CHIP_COLORS[status] ?? 'default'} size="sm" variant="soft">
      {t(`payment.status_${status}`, status)}
    </Chip>
  );
}

export default function RechargePage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const site = useSiteSettings();

  // 表单状态：预设金额 / 自定义金额 / 支付方式
  const [presetAmount, setPresetAmount] = useState<number | null>(PRESET_AMOUNTS[0] ?? null);
  const [customAmount, setCustomAmount] = useState('');
  const [method, setMethod] = useState('');

  // 当前正在支付的订单（下单成功或从记录「继续支付」进入）
  const [activeOrder, setActiveOrder] = useState<PaymentOrder | null>(null);
  const [qrDataUrl, setQrDataUrl] = useState('');
  // 已处理过 paid 状态的订单号，避免重复 invalidate/toast
  const paidHandledRef = useRef<string | null>(null);

  // 可用支付方式
  const { data: methodsData, isLoading: methodsLoading } = useQuery({
    queryKey: queryKeys.paymentMethods(),
    queryFn: () => paymentApi.methods(),
  });

  // 充值记录（分页）
  const { page, setPage, pageSize, setPageSize } = usePagination(DEFAULT_PAGE_SIZE, 'user.recharge');
  const {
    data: ordersData,
    isFetching: ordersFetching,
    isLoading: ordersLoading,
    refetch: refetchOrders,
  } = useQuery({
    queryKey: queryKeys.paymentOrders({ page, page_size: pageSize }),
    queryFn: () => paymentApi.listOrders({ page, page_size: pageSize }),
    placeholderData: keepPreviousData,
  });

  // 支付状态轮询：活跃订单为 pending 时每 3s 查一次，查到终态即停
  const activeNo = activeOrder?.out_trade_no ?? '';
  const { data: polledOrder } = useQuery({
    queryKey: queryKeys.paymentOrder(activeNo),
    queryFn: () => paymentApi.getOrder(activeNo),
    enabled: !!activeOrder && activeOrder.status === 'pending',
    refetchInterval: (query) => (query.state.data && query.state.data.status !== 'pending' ? false : 3000),
    meta: { globalLoading: false },
  });

  // 展示用订单：轮询结果优先（状态最新），否则用下单返回的快照
  const displayOrder = polledOrder && polledOrder.out_trade_no === activeNo ? polledOrder : activeOrder;
  const displayStatus = displayOrder?.status;

  // 下单
  const createMutation = useCrudMutation<PaymentOrder, CreatePaymentOrderReq>({
    mutationFn: (data) => paymentApi.createOrder(data),
    queryKey: queryKeys.paymentOrders(),
    onSuccess: (order) => setActiveOrder(order),
  });

  // 兑换码兑换
  const [redeemCode, setRedeemCode] = useState('');
  const redeemMutation = useCrudMutation<RedeemResp, string>({
    mutationFn: (code) => redemptionApi.redeem(code),
    queryKey: queryKeys.userMe(),
    onSuccess: (result) => {
      setRedeemCode('');
      toast('success', t('redemption.redeem_success', {
        value: result.value.toFixed(2),
        balance: result.balance.toFixed(2),
      }));
      queryClient.invalidateQueries({ queryKey: queryKeys.myBalanceHistory() });
    },
  });

  function handleRedeem() {
    const code = redeemCode.trim();
    if (!code) {
      toast('error', t('redemption.code_required'));
      return;
    }
    redeemMutation.mutate(code);
  }

  // 支付成功 → 刷新余额相关缓存（余额展示来自会话用户，提示刷新页面）
  useEffect(() => {
    if (!displayOrder || displayOrder.status !== 'paid') return;
    if (paidHandledRef.current === displayOrder.out_trade_no) return;
    paidHandledRef.current = displayOrder.out_trade_no;
    queryClient.invalidateQueries({ queryKey: queryKeys.userMe() });
    queryClient.invalidateQueries({ queryKey: queryKeys.myBalanceHistory() });
    queryClient.invalidateQueries({ queryKey: queryKeys.paymentOrders() });
  }, [displayOrder, queryClient]);

  // 二维码生成：优先 qr_code_content，缺省回退 payment_url
  const qrContent = displayOrder?.qr_code_content || displayOrder?.payment_url || '';
  useEffect(() => {
    let cancelled = false;
    if (!qrContent) {
      setQrDataUrl('');
      return;
    }
    QRCode.toDataURL(qrContent, { width: 240, margin: 1 })
      .then((url) => {
        if (!cancelled) setQrDataUrl(url);
      })
      .catch(() => {
        if (!cancelled) setQrDataUrl('');
      });
    return () => {
      cancelled = true;
    };
  }, [qrContent]);

  const methods = methodsData?.methods ?? [];
  const configured = methodsData?.configured ?? false;
  const selectedMethod = method || methods[0]?.key || '';

  // 有效充值金额：自定义输入优先，否则取预设
  const effectiveAmount = customAmount.trim() ? Number(customAmount) : presetAmount ?? 0;

  function methodLabel(key: string): string {
    const info = methods.find((m) => m.key === key);
    return t(`payment.method_${key}`, info?.label || key);
  }

  function handlePay() {
    if (!Number.isFinite(effectiveAmount) || effectiveAmount <= 0) {
      toast('error', t('payment.amount_invalid'));
      return;
    }
    if (!selectedMethod) {
      toast('error', t('payment.select_method'));
      return;
    }
    createMutation.mutate({ amount: effectiveAmount, method: selectedMethod });
  }

  function closeModal() {
    setActiveOrder(null);
  }

  const payModalState = useOverlayState({
    isOpen: !!activeOrder,
    onOpenChange: (open) => {
      if (!open) closeModal();
    },
  });

  const rows = ordersData?.list ?? [];
  const total = ordersData?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6">
      {/* 充值表单 */}
      <Card>
        <Card.Header>
          <Card.Title className="flex items-center gap-2">
            <Wallet className="h-4 w-4 text-primary" />
            {t('nav.recharge')}
          </Card.Title>
        </Card.Header>
        <Card.Content>
          {methodsLoading ? (
            <div className="flex items-center justify-center py-10">
              <Spinner size="sm" />
            </div>
          ) : !configured ? (
            <Alert status="warning">
              <Alert.Indicator>
                <AlertTriangle className="h-4 w-4" />
              </Alert.Indicator>
              <Alert.Content>
                <Alert.Title>{t('payment.not_configured_title')}</Alert.Title>
                <Alert.Description>{t('payment.not_configured_desc')}</Alert.Description>
              </Alert.Content>
            </Alert>
          ) : (
            <div className="space-y-5">
              {/* 汇率说明：优先展示管理员配置的提示文案，未配置则回退默认文案 */}
              <p className="text-sm text-text-tertiary">{site.recharge_notice || t('payment.recharge_note')}</p>

              {/* 金额选择 */}
              <div>
                <p className="mb-2 text-sm font-medium text-text">{t('payment.amount_label')}</p>
                <div className="flex flex-wrap items-center gap-2">
                  {PRESET_AMOUNTS.map((amount) => {
                    const selected = !customAmount.trim() && presetAmount === amount;
                    return (
                      <Button
                        key={amount}
                        size="sm"
                        variant={selected ? 'primary' : 'secondary'}
                        onPress={() => {
                          setPresetAmount(amount);
                          setCustomAmount('');
                        }}
                      >
                        ${amount.toFixed(2)}
                      </Button>
                    );
                  })}
                  <div className="w-36">
                    <Input
                      aria-label={t('payment.custom_amount_placeholder')}
                      min={0}
                      placeholder={t('payment.custom_amount_placeholder')}
                      type="number"
                      value={customAmount}
                      onChange={(e) => setCustomAmount(e.target.value)}
                    />
                  </div>
                </div>
              </div>

              {/* 支付方式选择 */}
              <div>
                <p className="mb-2 text-sm font-medium text-text">{t('payment.method_label')}</p>
                <div className="flex flex-wrap gap-2">
                  {methods.map((m) => (
                    <Button
                      key={m.key}
                      size="sm"
                      variant={selectedMethod === m.key ? 'primary' : 'secondary'}
                      onPress={() => setMethod(m.key)}
                    >
                      <QrCode className="h-4 w-4" />
                      {methodLabel(m.key)}
                    </Button>
                  ))}
                </div>
              </div>

              {/* 立即支付 */}
              <div className="flex items-center gap-3">
                <Button
                  aria-busy={createMutation.isPending}
                  isDisabled={createMutation.isPending}
                  variant="primary"
                  onPress={handlePay}
                >
                  {createMutation.isPending ? <Spinner size="sm" /> : <Wallet className="h-4 w-4" />}
                  {t('payment.pay_now')}
                </Button>
                <span className="font-mono text-sm text-text-secondary">
                  ${Number.isFinite(effectiveAmount) && effectiveAmount > 0 ? effectiveAmount.toFixed(2) : '0.00'}
                </span>
              </div>
            </div>
          )}
        </Card.Content>
      </Card>

      {/* 兑换码 */}
      <Card>
        <Card.Header>
          <Card.Title className="flex items-center gap-2">
            <Ticket className="h-4 w-4 text-primary" />
            {t('redemption.redeem_title')}
          </Card.Title>
        </Card.Header>
        <Card.Content>
          <div className="space-y-3">
            <p className="text-sm text-text-tertiary">{t('redemption.redeem_note')}</p>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
              <div className="w-full sm:max-w-sm">
                <Input
                  aria-label={t('redemption.redeem_placeholder')}
                  className="font-mono"
                  maxLength={64}
                  placeholder={t('redemption.redeem_placeholder')}
                  value={redeemCode}
                  onChange={(e) => setRedeemCode(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') handleRedeem();
                  }}
                />
              </div>
              <Button
                aria-busy={redeemMutation.isPending}
                isDisabled={redeemMutation.isPending || !redeemCode.trim()}
                variant="primary"
                onPress={handleRedeem}
              >
                {redeemMutation.isPending ? <Spinner size="sm" /> : <Ticket className="h-4 w-4" />}
                {t('redemption.redeem_submit')}
              </Button>
            </div>
          </div>
        </Card.Content>
      </Card>

      {/* 充值记录 */}
      <div>
        <div className="mb-3 flex items-center justify-between">
          <h3 className="text-base font-semibold text-text">{t('payment.records_title')}</h3>
          <RefreshButton
            ariaLabel={t('common.refresh', 'Refresh')}
            isRefreshing={ordersFetching}
            onRefresh={refetchOrders}
          />
        </div>
        <CommonTable
          ariaLabel={t('payment.records_title')}
          footer={(
            <TablePaginationFooter
              page={page}
              pageSize={pageSize}
              setPage={setPage}
              setPageSize={setPageSize}
              total={total}
              totalPages={totalPages}
            />
          )}
          minWidth={720}
        >
          <CommonTable.Header>
            <CommonTable.Column id="created_at">{t('payment.order_time')}</CommonTable.Column>
            <CommonTable.Column id="out_trade_no">{t('payment.order_no')}</CommonTable.Column>
            <CommonTable.Column id="method">{t('payment.order_method')}</CommonTable.Column>
            <CommonTable.Column id="amount">{t('payment.order_amount')}</CommonTable.Column>
            <CommonTable.Column id="status">{t('payment.order_status')}</CommonTable.Column>
            <CommonTable.Column id="actions" style={{ width: 120 }}>{t('common.actions')}</CommonTable.Column>
          </CommonTable.Header>
          <CommonTable.Body>
            {ordersLoading ? (
              <TableLoadingRow colSpan={6} />
            ) : rows.length === 0 ? (
              <CommonTable.Row id="empty">
                <CommonTable.Cell colSpan={6}>
                  <EmptyState>
                    <div className="text-sm text-default-500">{t('common.no_data')}</div>
                  </EmptyState>
                </CommonTable.Cell>
              </CommonTable.Row>
            ) : (
              rows.map((row) => {
                const canContinue = row.status === 'pending' && !!(row.payment_url || row.qr_code_content);
                return (
                  <CommonTable.Row id={row.out_trade_no} key={row.out_trade_no}>
                    <CommonTable.Cell>{formatDateTime(row.created_at)}</CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="font-mono text-xs text-text-secondary">{row.out_trade_no}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>{methodLabel(row.method)}</CommonTable.Cell>
                    <CommonTable.Cell>
                      <span className="font-mono">${row.amount.toFixed(2)}</span>
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      <PaymentStatusChip status={row.status} />
                    </CommonTable.Cell>
                    <CommonTable.Cell>
                      {canContinue ? (
                        <Button size="sm" variant="secondary" onPress={() => setActiveOrder(row)}>
                          <QrCode className="h-3.5 w-3.5" />
                          {t('payment.continue_pay')}
                        </Button>
                      ) : null}
                    </CommonTable.Cell>
                  </CommonTable.Row>
                );
              })
            )}
          </CommonTable.Body>
        </CommonTable>
      </div>

      {/* 扫码支付弹窗：pending 扫码轮询 / paid 成功卡片 / expired 失败卡片 */}
      <Modal state={payModalState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="sm">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>
                  {displayStatus === 'paid'
                    ? t('payment.paid_title')
                    : displayStatus === 'expired'
                      ? t('payment.expired_title')
                      : t('payment.scan_title')}
                </Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                {displayStatus === 'paid' ? (
                  <div className="flex flex-col items-center gap-3 py-6 text-center">
                    <CheckCircle className="h-12 w-12 text-success" />
                    <p className="font-mono text-2xl font-semibold text-text">
                      ${(displayOrder?.amount ?? 0).toFixed(2)}
                    </p>
                    <p className="text-sm text-text-secondary">{t('payment.paid_desc')}</p>
                  </div>
                ) : displayStatus === 'expired' ? (
                  <div className="flex flex-col items-center gap-3 py-6 text-center">
                    <Clock className="h-12 w-12 text-text-tertiary" />
                    <p className="text-sm text-text-secondary">{t('payment.expired_desc')}</p>
                  </div>
                ) : (
                  <div className="flex flex-col items-center gap-3 py-2 text-center">
                    <p className="text-sm text-text-secondary">
                      {t('payment.scan_desc', { method: methodLabel(displayOrder?.method ?? '') })}
                    </p>
                    <p className="font-mono text-2xl font-semibold text-text">
                      ${(displayOrder?.amount ?? 0).toFixed(2)}
                    </p>
                    {qrDataUrl ? (
                      <img
                        alt={t('payment.scan_title')}
                        className="h-60 w-60 rounded-[var(--radius)] border border-border bg-white p-2"
                        src={qrDataUrl}
                      />
                    ) : (
                      <div className="flex h-60 w-60 items-center justify-center rounded-[var(--radius)] border border-border">
                        <span className="text-sm text-text-tertiary">{t('payment.qr_generating')}</span>
                      </div>
                    )}
                    {displayOrder?.payment_url ? (
                      <a
                        className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
                        href={displayOrder.payment_url}
                        rel="noopener noreferrer"
                        target="_blank"
                      >
                        <ExternalLink className="h-3.5 w-3.5" />
                        {t('payment.open_payment_page')}
                      </a>
                    ) : null}
                    {displayOrder?.expires_at ? (
                      <p className="text-xs text-text-tertiary">
                        {t('payment.expires_at_hint', { time: formatDateTime(displayOrder.expires_at) })}
                      </p>
                    ) : null}
                    <div className="flex items-center gap-1.5 text-xs text-text-tertiary">
                      <Spinner size="sm" />
                      {t('payment.status_pending')}
                    </div>
                  </div>
                )}
              </Modal.Body>
              <Modal.Footer>
                {displayStatus === 'expired' ? (
                  <Button variant="primary" onPress={closeModal}>
                    {t('payment.recharge_again')}
                  </Button>
                ) : (
                  <Button variant={displayStatus === 'paid' ? 'primary' : 'secondary'} onPress={closeModal}>
                    {displayStatus === 'paid' ? t('common.close') : t('common.cancel')}
                  </Button>
                )}
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </div>
  );
}
