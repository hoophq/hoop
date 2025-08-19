(ns webapp.plugins.views.plugin-configurations.slack
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   ["@radix-ui/themes" :refer [Button]]
   [webapp.components.forms :as forms]
   [webapp.components.headings :as h]
   [webapp.components.tabs :as tabs]
   [webapp.config :as config]
   [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]))

(defn base64-decode-safe [s]
  (try
    (if (and (string? s) (seq s))
      (js/atob s)
      s)
    (catch :default _
      s)))

(defn configuration-modal [{:keys [connection plugin]}]
  (let [current-connection-config (first (filter #(= (:id connection)
                                                     (:id %))
                                                 (:connections plugin)))
        slack-channels-value (r/atom (or (str/join ", " (:config current-connection-config)) ""))]
    (fn [{:keys [plugin]}]
      [:section
       {:class "flex flex-col px-small pt-regular"}
       [:form
        {:on-submit (fn [e]
                      (.preventDefault e)
                      (let [connection  (merge current-connection-config
                                               {:config (map str/trim (str/split @slack-channels-value #","))})
                            dissoced-connections (filter #(not= (:id %)
                                                                (:id connection))
                                                         (:connections plugin))
                            new-plugin-data (assoc plugin :connections (conj
                                                                        dissoced-connections
                                                                        connection))]
                        (rf/dispatch [:plugins->update-plugin new-plugin-data])))}
        [:header
         [:div {:class "font-bold text-xs"}
          "Slack channels"]
         [:footer {:class "text-xs text-gray-600 pb-1"}
          "Provide slack channels to receive connection reviews."]]
        [forms/input {:value @slack-channels-value
                      :id "slack-channels"
                      :name "slack-channels"
                      :required true
                      :on-change #(reset! slack-channels-value (-> % .-target .-value))
                      :placeholder "C039AQNN5DF, C031T9LDGAH"}]
        [:footer
         {:class "flex justify-end"}
         [:div {:class "flex-shrink"}
          [:> Button {:type "submit"
                      :size "2"
                      :variant "solid"}
           "Save"]]]]])))

(defn- configurations-view [plugin-details]
  (let [envvars (-> @plugin-details :plugin :config :envvars)
        slack-bot-token (r/atom (or (base64-decode-safe (:SLACK_BOT_TOKEN envvars)) ""))
        slack-app-token (r/atom (or (base64-decode-safe (:SLACK_APP_TOKEN envvars)) ""))]
    (fn []
      [:section
       [:div {:class "grid grid-cols-3 gap-large my-large"}
        [:div {:class "col-span-1"}
         [h/h3 "Slack App Configurations" {:class "text-gray-800"}]
         [:span {:class "text-sm text-gray-600"}
          "Here you will integrate with your Slack App. Please visit our doc to "]
         [:a {:href (get-in config/docs-url [:integrations :slack])
              :target "_blank"
              :class "font-semibold text-sm text-gray-700 underline"}
          [:span "learn how to create a Slack App."]]]
        [:div {:class "col-span-2"}
         [:form
          {:class "mb-regular"
           :on-submit (fn [e]
                        (.preventDefault e)
                        (rf/dispatch [:slack-plugin->slack-config {:slack-bot-token @slack-bot-token
                                                                   :slack-app-token @slack-app-token}]))}
          [:div {:class "grid gap-regular"}
           [forms/input {:label "Slack bot token"
                         :on-change #(reset! slack-bot-token (-> % .-target .-value))
                         :classes "whitespace-pre overflow-x"
                         :type "password"
                         :value @slack-bot-token}]
           [forms/input {:label "Slack app token"
                         :on-change #(reset! slack-app-token (-> % .-target .-value))
                         :classes "whitespace-pre overflow-x"
                         :type "password"
                         :value @slack-app-token}]]
          [:div {:class "flex justify-end"}
           [:> Button {:type "submit"
                       :size "3"
                       :variant "solid"}
            "Save"]]]]]])))

(defmulti ^:private selected-view identity)
(defmethod ^:private selected-view :Connections [_ _]
  [plugin-configuration-container/main configuration-modal])

(defmethod ^:private selected-view :Configurations [_ plugin-details]
  [configurations-view plugin-details])

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        selected-tab (r/atom :Connections)]
    (fn []
      [:div
       [tabs/tabs {:on-change #(reset! selected-tab %)
                   :tabs [:Connections :Configurations]}]
       [selected-view @selected-tab plugin-details]])))

