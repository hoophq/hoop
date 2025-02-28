import { useCallback, useState } from 'react';
export const useModal = () => {
  const [modal, setModal] = useState({
    isOpen: false,
    component: null
  });
  const openModal = useCallback(component => {
    setModal({
      isOpen: true,
      component
    });
  }, [setModal]);
  const closeModal = useCallback(() => {
    setModal({
      isOpen: false,
      component: null
    });
  }, [setModal]);
  return {
    isOpen: modal.isOpen,
    component: modal.component,
    openModal,
    closeModal
  };
};