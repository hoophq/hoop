(ns webapp.connections.views.setup.network
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading RadioGroup Text]]
   ["lucide-react" :refer [Network]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(def network-types
  [{:id "tcp" :title "TCP"}
   {:id "http" :title "HTTP (soon)" :disabled true}])

(defn- render-http-headers-form []
  (let [headers (r/atom [])
        current-key (r/atom "")
        current-value (r/atom "")]
    (fn []
      [:> Box {:class "mt-4 space-y-4"}
       [:> Text {:size "3" :weight "medium"} "HTTP headers"]

       ;; Existing headers list
       (for [{:keys [key value]} @headers]
         ^{:key key}
         [:> Grid {:columns "12" :gap "2" :class "items-center"}
          [:> Box {:class "col-span-5"}
           [:> Text {:size "2"} key]]
          [:> Box {:class "col-span-5"}
           [:> Text {:size "2"} value]]
          [:> Box {:class "col-span-2"}
           [:> Button {:size "1"
                       :variant "soft"
                       :color "red"
                       :on-click #(swap! headers (fn [h] (remove
                                                          (fn [header] (= (:key header) key)) h)))}
            "Remove"]]])

       ;; Add new header form
       [:> Grid {:columns "12" :gap "2"}
        [:> Box {:class "col-span-5"}
         [forms/input
          {:size "2"
           :placeholder "Key"
           :value @current-key
           :on-change #(reset! current-key (-> % .-target .-value))}]]
        [:> Box {:class "col-span-5"}
         [forms/input
          {:size "2"
           :placeholder "Value"
           :value @current-value
           :on-change #(reset! current-value (-> % .-target .-value))}]]
        [:> Box {:class "col-span-2"}
         [:> Button
          {:size "2"
           :variant "soft"
           :on-click #(when (and (not-empty @current-key) (not-empty @current-value))
                        (swap! headers conj {:key @current-key :value @current-value})
                        (reset! current-key "")
                        (reset! current-value ""))}
          "Add"]]]])))

(defn- credentials-form []
  (let [selected-type @(rf/subscribe [:connection-setup/network-type])
        credentials @(rf/subscribe [:connection-setup/network-credentials])]
    [:> Box {:class "space-y-5"}
     [:> Text {:size "4" :weight "bold"} "Environment credentials"]

     ;; Host input
     [forms/input
      {:label "Host"
       :placeholder "e.g. localhost"
       :required true
       :value (get credentials :host "")
       :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                 :host
                                 (-> % .-target .-value)])}]

     ;; User input
     [forms/input
      {:label "User"
       :placeholder "e.g. username"
       :value (get credentials :user "")
       :on-change #(rf/dispatch [:connection-setup/update-network-credentials
                                 :user
                                 (-> % .-target .-value)])}]

     ;; HTTP Headers section (only for HTTP type)
     (when (= selected-type "http")
       [render-http-headers-form])]))

(defn- resource-step []
  (let [selected-type @(rf/subscribe [:connection-setup/network-type])]
    [:> Box {:class "space-y-5"}
     [:> Text {:size "4" :weight "bold"} "Network access type"]
     [:> RadioGroup.Root {:name "network-type"
                          :value selected-type
                          :on-value-change #(rf/dispatch [:connection-setup/select-network-type %])}
      [:> Grid {:columns "1" :gap "3"}
       (for [{:keys [id title disabled]} network-types]
         ^{:key id}
         [:> RadioGroup.Item
          {:value id
           :class (str "p-4 " (when disabled "opacity-50 cursor-not-allowed"))
           :disabled disabled}
          [:> Flex {:gap "3" :align "center"}
           [:> Network {:size 16}]
           title]])]]

     (when selected-type
       [credentials-form])]))

(defn main []
  (let [current-step @(rf/subscribe [:connection-setup/current-step])
        selected-type @(rf/subscribe [:connection-setup/network-type])]
    [page-wrapper/main
     {:children [:> Box {:class "min-h-screen bg-gray-1"}
                     ;; Main content with padding to account for fixed footer
                 [:> Box {:class "pb-24"}
                  [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                   [headers/setup-header]

                   (case current-step
                     :resource [resource-step]
                     :additional-config [additional-configuration/main
                                         {:selected-type @(rf/subscribe [:connection-setup/network-type])}]
                     [resource-step])]]]

                     ;; Footer
      :footer-props {:next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next: Configuration")
                     :next-disabled? (or (not selected-type)
                                         false
                                         #_(and (= current-step :resource)
                                                (not network-valid?)))
                     :on-next (if (= current-step :additional-config)
                                #(rf/dispatch [:connection-setup/submit])
                                #(rf/dispatch [:connection-setup/next-step]))}}]))
