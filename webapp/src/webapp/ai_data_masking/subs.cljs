(ns webapp.ai-data-masking.subs
  (:require
   [re-frame.core :as rf]))

;; AI Data Masking list
(rf/reg-sub
 :ai-data-masking->list
 (fn [db _]
   (get-in db [:ai-data-masking :list])))

;; Active AI Data Masking rule
(rf/reg-sub
 :ai-data-masking->active-rule
 (fn [db _]
   (get-in db [:ai-data-masking :active-rule])))

;; Submitting state
(rf/reg-sub
 :ai-data-masking->submitting?
 (fn [db _]
   (get-in db [:ai-data-masking :submitting?])))
