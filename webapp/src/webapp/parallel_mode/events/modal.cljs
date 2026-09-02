(ns webapp.parallel-mode.events.modal
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]))

;; ---- Search ----
;; The gateway matches ?search= with ILIKE '%term%' over name, subtype, type,
;; resource_name and status. cmdk's client-side scorer is off (see
;; components/modal/main.cljs :should-filter? false), so this is the only
;; matcher. EVL-243.

(def ^:private search-debounce-ms 300)

(defonce ^:private search-timer (atom nil))

(defn- cancel-search-timer! []
  (when-let [timer @search-timer]
    (js/clearTimeout timer)
    (reset! search-timer nil)))

(rf/reg-fx
 :parallel-mode/search-connections
 (fn [term]
   (cancel-search-timer!)
   (reset! search-timer
           (js/setTimeout
            (fn []
              (reset! search-timer nil)
              (rf/dispatch [:connections/get-connections-paginated
                            (cond-> {:page 1 :force-refresh? true}
                              (not (cs/blank? term)) (assoc :search term))]))
            search-debounce-ms))))

(rf/reg-fx
 :parallel-mode/cancel-connections-search
 (fn [_]
   (cancel-search-timer!)))

;; ---- Modal Control Events ----

(rf/reg-event-db
 :parallel-mode/open-modal
 (fn [db [_ source]]
   (let [current-connections (get-in db [:parallel-mode :selection :connections])]
     (-> db
         (update-in [:parallel-mode :modal] merge {:open? true
                                                   :search-term ""
                                                   :source source})
         (assoc-in [:parallel-mode :selection :draft-connections] current-connections)))))

;; Every close path goes through here. :connections->pagination is one global
;; slice that the terminal picker, /resources and the audit filters also read,
;; so this modal's search must not outlive the modal.
(rf/reg-event-fx
 :parallel-mode/close-modal
 (fn [{:keys [db]} _]
   (let [searched? (not (cs/blank? (get-in db [:parallel-mode :modal :search-term])))]
     (cond-> {:db (update-in db [:parallel-mode :modal] merge {:open? false :search-term ""})
              :parallel-mode/cancel-connections-search true}
       searched?
       (assoc :fx [[:dispatch [:connections/get-connections-paginated
                               {:page 1 :force-refresh? true}]]])))))

;; The seed runs here, not in a second dispatch from the button, so it always
;; lands before :parallel-mode/open-modal takes the Cancel snapshot.
(rf/reg-event-fx
 :parallel-mode/toggle-modal
 (fn [{:keys [db]} [_ source]]
   (let [currently-open? (get-in db [:parallel-mode :modal :open?])]
     (if currently-open?
       {:fx [[:dispatch [:parallel-mode/close-modal]]]}
       {:fx [[:dispatch [:parallel-mode/seed-from-host source]]
             [:dispatch [:parallel-mode/open-modal source]]
             [:dispatch [:connections/get-connections-paginated {:page 1 :force-refresh? true}]]]}))))

; Removed step management - direct connection selection only

(rf/reg-event-fx
 :parallel-mode/set-search-term
 (fn [{:keys [db]} [_ term]]
   {:db (assoc-in db [:parallel-mode :modal :search-term] term)
    :parallel-mode/search-connections term}))
