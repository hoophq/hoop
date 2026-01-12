(ns webapp.audit.views.sessions-filtered-by-id
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]
            [webapp.config :as config]
            [webapp.audit.views.session-item :as session-item]
            [webapp.components.loaders :as loaders]))

(defn empty-list-view []
  [:div {:class "pt-x-large"}
   [:figure
    {:class "w-1/6 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/pc.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "Beep boop, no sessions to look"]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "There's nothing with this criteria"]]])

(defn loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn get-query-params []
  (let [search (.. js/window -location -search)
        params (js/URLSearchParams. search)]
    {:batch-id (.get params "batch_id")
     :id (.get params "id")}))

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        session-list (rf/subscribe [:audit->filtered-session-by-id])
        initialized (atom false)]

    (when (empty? (:data @user))
      (rf/dispatch [:users->get-user]))

    (fn []
      ;; Check query params and dispatch appropriate event on mount
      (when-not @initialized
        (reset! initialized true)
        (let [{:keys [batch-id id]} (get-query-params)]
          (cond
            (not (cs/blank? batch-id))
            (rf/dispatch [:audit->get-sessions-by-batch-id batch-id])
            
            (not (cs/blank? id))
            (let [session-ids (cs/split id #",")]
              (rf/dispatch [:audit->get-filtered-sessions-by-id session-ids])))))
      
      (let [has-sessions? (seq (:data @session-list))
            is-ready? (= (:status @session-list) :ready)
            is-loading? (= (:status @session-list) :loading)]
        (js/console.log "🎨 Rendering filtered view" 
                        "status:" (:status @session-list)
                        "data count:" (count (:data @session-list))
                        "has-sessions?" has-sessions?
                        "is-ready?" is-ready?)
        [:div {:class "px-large flex flex-col bg-white rounded-lg py-regular h-full"}
         (when (and is-loading? (empty? (:data @session-list)))
           [loading-list-view])
         
         (when (and is-ready? (not has-sessions?))
           [empty-list-view])

         (when (and is-ready? has-sessions?)
           [:div {:class "relative border h-full rounded-lg overflow-auto"}
            (doall
             (for [session (:data @session-list)]
               ^{:key (:id session)}
               [:div
                [session-item/session-item session @user]]))])]))))

