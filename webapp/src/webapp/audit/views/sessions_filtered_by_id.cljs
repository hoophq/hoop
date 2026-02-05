(ns webapp.audit.views.sessions-filtered-by-id
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   ["@radix-ui/themes" :refer [Box Button Flex Heading]]
   ["lucide-react" :refer [Search Link]]
   [webapp.audit.views.session-item :as session-item]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]
   [webapp.config :as config]))

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
        filtered-sessions (rf/subscribe [:audit->filtered-sessions-by-id-filtered])
        search-term (rf/subscribe [:audit->filtered-session-search-term])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])
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
            has-filtered-sessions? (seq @filtered-sessions)
            is-ready? (= (:status @session-list) :ready)
            is-loading? (= (:status @session-list) :loading)
            loading-more? (:loading @session-list)
            has-more? (:has-more? @session-list)
            {:keys [batch-id]} (get-query-params)]
        [:div {:class "flex flex-col bg-white h-full"}
         (when (and is-loading? (empty? (:data @session-list)))
           [loading-list-view])

         (when (and is-ready? (not has-sessions?))
           [empty-list-view])

         (when (and is-ready? has-sessions?)
           [:div {:class "relative h-full pb-6 px-6 overflow-auto"}
            [:> Flex {:justify "between" :align "center" :gap "4" :class "pt-6 pb-8 sticky top-0 bg-white z-10"}
             [:> Flex {:align "center" :gap "5"}
              [:> Heading {:size "5" :weight "bold" :class "text-gray-12"}
               "Execution Summary"]

              (when-not @clipboard-disabled?
                [:> Button
                 {:size "2"
                  :variant "soft"
                  :color "gray"
                  :highContrast true
                  :disabled (nil? batch-id)
                  :onClick (when batch-id
                             #(let [url (str (.. js/window -location -origin) "/sessions/filtered?batch_id=" batch-id)]
                                (-> js/navigator
                                    .-clipboard
                                    (.writeText url)
                                    (.then (fn []
                                             (rf/dispatch [:show-snackbar {:level :success
                                                                           :text "Link copied to clipboard!"}])))
                                    (.catch (fn [err]
                                              (js/console.error "Failed to copy:" err)
                                              (rf/dispatch [:show-snackbar {:level :error
                                                                            :text "Failed to copy link"}]))))))}
                 [:> Link {:size 16}]
                 "Share"])]

             [:> Flex {:align "center" :gap "3"}
              ;; Search
              [:> Box {:class "w-64"}
               [forms/input
                {:size "2"
                 :not-margin-bottom? true
                 :placeholder "Search by resource role or type"
                 :value @search-term
                 :on-change #(rf/dispatch [:audit->set-filtered-session-search (.. % -target -value)])
                 :start-adornment [:> Search {:size 16}]}]]]]

            (if has-filtered-sessions?
              [infinite-scroll
               {:on-load-more (fn []
                                (when (and batch-id has-more? (not loading-more?))
                                  (rf/dispatch [:audit->get-sessions-by-batch-id-next-page batch-id])))
                :has-more? has-more?
                :loading? loading-more?}
               [:div {:class "rounded-lg border"}
                (doall
                 (for [session @filtered-sessions]
                   ^{:key (:id session)}
                   [:div
                    [session-item/session-item session @user]]))]]
              (when (and is-ready? (not (cs/blank? @search-term)))
                [:div {:class "p-6 text-center text-gray-500"}
                 "No sessions found matching your search"]))])]))))

