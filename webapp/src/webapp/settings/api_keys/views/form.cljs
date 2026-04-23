(ns webapp.settings.api-keys.views.form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.forms :as forms]
   [webapp.components.loaders :as loaders]
   [webapp.components.multiselect :as multi-select]))

(defn- form-section [{:keys [title description]} & children]
  [:> Grid {:columns "7" :gap "7"}
   [:> Box {:grid-column "span 2 / span 2"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
     title]
    [:> Text {:size "3" :class "text-[--gray-11]"}
     description]]
   (into [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}]
         children)])

(defn- groups->select-options [groups]
  (mapv #(hash-map :value % :label %) groups))

(defn- select-options->groups [options]
  (mapv :value (or options [])))

(defn- api-key-form [form-type ak-data scroll-pos]
  (let [name-val   (r/atom (or (:name ak-data) ""))
        groups-val (r/atom (groups->select-options (or (:groups ak-data) [])))
        user-groups (rf/subscribe [:user-groups])
        submitting? (rf/subscribe [:api-keys/submitting?])]
    (fn []
      [:> Box {:class "min-h-screen bg-gray-1"}
       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (let [body {:name @name-val
                                        :groups (select-options->groups @groups-val)}]
                              (if (= form-type :create)
                                (rf/dispatch [:api-keys/create body])
                                (rf/dispatch [:api-keys/update (:id ak-data) body]))))}

        [:<>
         [:> Flex {:p "5" :gap "2"}
          [button/HeaderBack]]

         [:> Box {:class (str "sticky top-0 z-50 bg-gray-1 px-7 py-7 "
                              (when (>= @scroll-pos 30)
                                "border-b border-[--gray-a6]"))}
          [:> Flex {:justify "between" :align "center"}
           [:> Heading {:as "h2" :size "8"}
            (if (= form-type :create)
              "Create new API Key"
              "Configure API Key")]
           [:> Button {:size "3"
                       :type "submit"
                       :disabled @submitting?}
            "Save"]]]

         [:> Box {:p "7" :class "space-y-radix-9"}
          [form-section {:title "Set basic information"
                         :description "Used to identify this API key and its actions across Hoop."}
           [forms/input {:label "Name"
                         :placeholder "e.g. AI Agent SRE"
                         :value @name-val
                         :required true
                         :class "w-full"
                         :autoFocus (= form-type :create)
                         :on-change #(reset! name-val (-> % .-target .-value))}]]

          [form-section {:title "Groups configuration"
                         :description "Select which groups this API Key will belong to."}
           [multi-select/main {:label "Groups"
                               :default-value @groups-val
                               :options (groups->select-options @user-groups)
                               :on-change #(reset! groups-val (js->clj % :keywordize-keys true))}]]]]]])))

(defn main [form-type]
  (let [active (rf/subscribe [:api-keys/active])
        scroll-pos (r/atom 0)]

    (rf/dispatch [:users->get-user-groups])

    (fn []
      (r/with-let [handle-scroll #(reset! scroll-pos (.-scrollY js/window))
                   _ (.addEventListener js/window "scroll" handle-scroll)]
        (let [ak-data  (if (= :configure form-type)
                         (:data @active)
                         {})
              loading? (and (= :configure form-type)
                            (= :loading (:status @active)))]
          (if loading?
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]
            ^{:key (str form-type "-" (:id ak-data))}
            [api-key-form form-type ak-data scroll-pos]))
        (finally
          (.removeEventListener js/window "scroll" handle-scroll))))))
