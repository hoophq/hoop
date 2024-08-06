(ns webapp.runbooks.views.template-view
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.audit.views.session-details :as session-details]
            [webapp.components.button :as button]
            [webapp.components.icon :as icon]
            [webapp.components.headings :as h]
            [webapp.runbooks.views.template-dynamic-form :as template]))

(defn- no-connections-enabled-view []
  [:div {:class "pt-large flex flex-col gap-regular items-center"}
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class "text-gray-700 text-base font-bold"}
     "No connections enabled."]
    [:div {:class "text-gray-500 text-sm mb-large"}
     "If you don't have connections to be selected, ask for the admin to enable it on the manage runbooks page."]]])

(defmulti template-view identity)
(defmethod template-view :ready [_ _ _]
  (let [state (r/atom {})
        update-state #(swap! state assoc %1 %2)]
    (rf/dispatch [:connections->get-connections])
    (fn [_ template connection-name]
      (let [all-connections (map #(into {} {:value % :text %})
                                 (-> template :data :connections))]

      ;; TODO: This implementation was made to fix the behavior of defmethod not accepting the re-rendering
      ;; based on its own key.
        (if (nil? (:data template))
          [:div {:class "flex items-center justify-center h-full"}
           [:span
            {:class (str "text-gray-400 text-xl")}
            "No Runbook selected"]]

          [:div {:class "overflow-auto lg:overflow-hidden"}
           [:section
            [:form
             {:on-submit (fn [e]
                           (.preventDefault e)
                           (rf/dispatch [:runbooks-plugin->run-runbook
                                         {:file-name (-> template :data :name)
                                          :params @state
                                          :connection-name connection-name}])
                           (rf/dispatch [:open-modal
                                         [session-details/main]
                                         :large
                                         (fn []
                                           (rf/dispatch [:audit->clear-session])
                                           (rf/dispatch [:close-modal]))]))}
             [:header {:class "mb-regular"}
              [h/h3 (-> template :data :name) {:class "break-words text-blue-500"}]

              [:span.text-gray-500.text-xs
               "Fill the params below for this Runbook"]]
             (doall (for [param (-> template :data :params)
                          :let [metadata ((keyword param) (-> template :data :metadata))]]
                      ^{:key param}
                      [template/dynamic-form
                       (:type metadata) {:label param
                                         :placeholder (:placeholder metadata)
                                         :value (get @state param)
                                         :type (:type metadata)
                                         :required (:required metadata)
                                         :on-change #(update-state param (-> % .-target .-value))
                                         :helper-text (:description metadata)
                                         :options (:options metadata)}]))

             (if (seq all-connections)
               [:footer {:class "flex gap-regular justify-end"}
                [button/primary {:text [:span {:class "flex gap-small items-center"}
                                        [:span "Run"]
                                        (if (= (-> template :form-status) :loading)
                                          [:figure {:class "w-4"}
                                           [:img {:class "animate-spin"
                                                  :src "/icons/icon-loader-circle-white.svg"}]]
                                          [icon/hero-icon {:size 6
                                                           :icon "play-circle-white"}])]
                                 :disabled (or (= (-> template :status) :loading)
                                               (= (-> template :form-status) :loading))
                                 :type "submit"}]]

               [no-connections-enabled-view])]]])))))

(defmethod template-view :loading []
  [:div {:class "flex items-center justify-center h-full"}
   [:figure {:class "w-8"}
    [:img {:class "animate-spin"
           :src "/icons/icon-loader-circle.svg"}]]])

(defmethod template-view :default []
  [:div {:class "flex items-center justify-center h-full"}
   [:span
    {:class (str "text-gray-400 text-xl")}
    "No template selected"]])

(defn main []
  (fn [{:keys [runbook preselected-connection]}]
    [template-view (:status runbook) runbook preselected-connection]))

