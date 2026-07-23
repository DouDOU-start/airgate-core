import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Input,
  Label,
  Modal,
  Spinner,
  TextArea,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Bookmark } from 'lucide-react';
import type {
  BookmarkResp,
  CreateBookmarkReq,
  UpdateBookmarkReq,
} from '../../../shared/types';

export function BookmarkFormModal({
  open,
  title,
  bookmark,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  title: string;
  bookmark?: BookmarkResp;
  onClose: () => void;
  onSubmit: (data: CreateBookmarkReq | UpdateBookmarkReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!bookmark;

  const buildForm = () => ({
    name: bookmark?.name ?? '',
    base_url: bookmark?.base_url ?? '',
    remark: bookmark?.remark ?? '',
  });

  const [form, setForm] = useState(buildForm);

  useEffect(() => {
    if (!open) {
      setForm(buildForm());
    }
  }, [open, bookmark]);

  const handleSubmit = () => {
    if (!form.name) return;
    onSubmit({
      name: form.name,
      base_url: form.base_url,
      remark: form.remark,
    });
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '540px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('bookmarks.name')}</Label>
                  <div className="relative">
                    <Bookmark className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={form.name}
                      onChange={(e) => setForm({ ...form, name: e.target.value })}
                      required
                    />
                  </div>
                </HeroTextField>

                <HeroTextField fullWidth>
                  <Label>{t('bookmarks.base_url')}</Label>
                  <Input
                    value={form.base_url}
                    onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                    placeholder="https://api.example.com"
                  />
                </HeroTextField>

                <HeroTextField fullWidth>
                  <Label>{t('bookmarks.remark')}</Label>
                  <TextArea
                    rows={4}
                    value={form.remark}
                    onChange={(e) => setForm({ ...form, remark: e.target.value })}
                  />
                </HeroTextField>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {isEdit ? t('common.save') : t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
