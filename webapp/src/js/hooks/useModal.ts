import { useCallback, useState } from 'react';

interface ModalState {
  isOpen: boolean;
  component: React.ComponentType<any> | null;
}

export const useModal = () => {
  const [modal, setModal] = useState<ModalState>({ isOpen: false, component: null });

  const openModal = useCallback((component: React.ComponentType<any>) => {
    setModal({ isOpen: true, component });
  }, [setModal]);

  const closeModal = useCallback(() => {
    setModal({ isOpen: false, component: null });
  }, [setModal]);

  return {
    isOpen: modal.isOpen,
    component: modal.component,
    openModal,
    closeModal,
  };
}; 
