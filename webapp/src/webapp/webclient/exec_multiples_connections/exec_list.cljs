(ns webapp.webclient.exec-multiples-connections.exec-list
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@heroicons/react/24/solid" :as hero-solid-icon]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]))

(def atom-exec-list-open? (r/atom false))

(defn ready-bar []
  [:div {:class "flex items-center w-36 justify-center gap-small rounded-md bg-gray-100 p-3 text-gray-900"}
   [:> hero-outline-icon/CheckIcon {:class "h-4 w-4 shrink-0"}]
   [:span {:class "text-xs"}
    "Ready"]])

(defn running-bar []
  [:div {:class "flex items-center w-36 justify-center gap-small rounded-md bg-gray-100 p-3 text-gray-900"}
   [:> hero-outline-icon/ArrowPathIcon {:class "h-4 w-4 shrink-0"}]
   [:span {:class "text-xs"}
    "Running"]])

(defn completed-bar []
  [:div {:class "flex items-center w-36 justify-center gap-small rounded-md bg-green-100 p-3 text-gray-900"}
   [:> hero-outline-icon/CheckIcon {:class "h-4 w-4 shrink-0"}]
   [:span {:class "text-xs"}
    "Completed"]])

(defn error-bar []
  [:div {:class "flex items-center w-36 justify-center gap-small rounded-md bg-red-100 p-3 text-gray-900"}
   [:> hero-outline-icon/ExclamationTriangleIcon {:class "h-4 w-4 shrink-0"}]
   [:span {:class "text-xs"}
    "Error"]])

(defn waiting-review-bar []
  [:div {:class "flex items-center w-36 justify-center gap-small rounded-md bg-yellow-100 p-3 text-gray-900"}
   [:> hero-outline-icon/ClockIcon {:class "h-4 w-4 shrink-0"}]
   [:span {:class "text-xs"}
    "Waiting Review"]])

(defn button-group-running []
  [:div {:class "mt-6 flex items-center justify-end gap-small"}
   [:span {:class "text-sm text-gray-500"}
    "Keep this screen open while your command is running"]
   [button/primary {:text [:div {:class "flex items-center gap-small"}
                           [:> hero-solid-icon/PlayIcon {:class "h-5 w-5 text-white"
                                                         :aria-hidden "true"}]
                           [:span "Run"]]
                    :disabled true
                    :type "button"
                    :on-click (fn [] false)}]])

(defn button-group-ready [exec-list-cold]
  [:div {:class "mt-6 flex items-center justify-end gap-small"}
   [button/secondary {:text "Close"
                      :type "button"
                      :on-click #(reset! atom-exec-list-open? false)}]
   [button/primary {:text [:div {:class "flex items-center gap-small"}
                           [:> hero-solid-icon/PlayIcon {:class "h-5 w-5 text-white"
                                                         :aria-hidden "true"}]
                           [:span "Run"]]
                    :disabled false
                    :type "button"
                    :on-click (fn []
                                (rf/dispatch [:editor-plugin->multiple-connections-exec-script
                                              (map #(assoc % :status :running) exec-list-cold)]))}]])

(defn button-group-completed [exec-list]
  [:div {:class "mt-6 flex items-center justify-end gap-small"}
   [button/secondary {:text "Close"
                      :type "button"
                      :on-click #(reset! atom-exec-list-open? false)}]
   [:a {:href (str (. (. js/window -location) -origin)
                   "/sessions/filtered?id="
                   (cs/join "," (map :session-id exec-list)))
        :target "_blank"
        :rel "noopener noreferrer"}
    [button/primary {:text "Open in a new tab"
                     :disabled false
                     :type "button"
                     :on-click (fn [] false)}]]])

(defn main [_]
  (let [exec-list (rf/subscribe [:editor-plugin->connections-exec-list])]
    (rf/dispatch [:editor-plugin->clear-connection-script])
    (fn [exec-list-cold]
      (let [current-exec-list (if (= (:status @exec-list) :ready)
                                exec-list-cold
                                (:data @exec-list))]
        [:div {:id "modal"
               :class "fixed z-50 inset-0 overflow-y-auto"
               "aria-modal" true}
         [:div {"aria-hidden" "true"
                :class "fixed w-full h-full inset-0 bg-black bg-opacity-80 transition"}]
         [:div {:class (str "relative mb-large m-auto "
                            "bg-white shadow-sm rounded-lg "
                            "mx-auto mt-16 lg:mt-large p-6 overflow-auto "
                            "w-full max-w-xs lg:max-w-4xl")}
          [:div
           [h/h4-md "Review and Run"
            {:class "mb-6"}]
           [:div {:class "border rounded-md"}
            (doall
             (for [exec current-exec-list]
               ^{:key (:connection-name exec)}
               [:div {:class "flex justify-between items-center gap-small p-regular border-b border-gray-200"}
                [:div {:class "flex flex-col gap-small"}
                 [:span {:class "text-sm text-gray-900 font-bold"}
                  (:connection-name exec)]
                 [:span {:class "text-xxs text-gray-900"}
                  (:subtype exec)]]

                [:div {:class "flex items-center gap-20"}
                 (when (:session-id exec)
                   [:div {:class "flex items-center gap-regular"}
                    [:span {:class "text-xs text-gray-500"}
                     "id:"]
                    [:span {:class "text-xs text-gray-900"}
                     (first (cs/split (:session-id exec) #"-"))]
                    [:a {:href (str (. (. js/window -location) -origin) "/sessions/" (:session-id exec))
                         :target "_blank"
                         :rel "noopener noreferrer"}
                     [:> hero-outline-icon/ArrowTopRightOnSquareIcon {:class "h-5 w-5 text-gray-900"}]]])

                 (case (:status exec)
                   :ready [ready-bar]
                   :running [running-bar]
                   :completed [completed-bar]
                   :error [error-bar]
                   :waiting-review [waiting-review-bar])]]))]
           (case (:status @exec-list)
             :ready [button-group-ready exec-list-cold]
             :running [button-group-running exec-list-cold]
             :completed [button-group-completed (:data @exec-list)])]]]))))
