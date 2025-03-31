(ns webapp.connections.views.tag-selector
  (:require ["lucide-react" :refer [Check ChevronRight ChevronLeft]]
            ["@radix-ui/themes" :refer [Box Button Flex Text Separator]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.searchbox :as searchbox]))

(rf/reg-event-fx
 :connections->get-connection-tags
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:connections :tags-loading] true)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/connection-tags"
                             :on-success (fn [response]
                                           (rf/dispatch [:connections->set-connection-tags (:items response)]))}]]]}))

(rf/reg-event-db
 :connections->set-connection-tags
 (fn [db [_ tags]]
   (-> db
       (assoc-in [:connections :tags] tags)
       (assoc-in [:connections :tags-loading] false))))

(rf/reg-sub
 :connections->tags
 (fn [db]
   (get-in db [:connections :tags])))

(rf/reg-sub
 :connections->tags-loading?
 (fn [db]
   (get-in db [:connections :tags-loading])))

(defn get-key-name [key]
  (let [parts (cs/split key #"/")]
    (last parts)))

(defn group-tags-by-key [tags]
  (reduce
   (fn [result tag]
     (let [key (:key tag)
           value (:value tag)]
       (update result key (fnil conj []) value)))
   {}
   tags))

(defn tags-to-query-string [selected-tags]
  (cs/join ","
           (for [[key values] selected-tags
                 value values]
             (str key "=" value))))

(defn get-display-key [key]
  (let [display-keys (cs/split (get-key-name key) ".")
        display-key (if (= (count display-keys) 1)
                      (first display-keys)
                      (second display-keys))]
    display-key))

(defn values-view [key values display-key selected-values on-change on-back]
  [:div {:class "w-full"}
   [:> Flex {:justify "between" :align "center" :class "mb-3"}
    [:> Button {:variant "ghost" :color "gray" :onClick on-back}
     [:> ChevronLeft {:size 16}]
     [:> Text {:size "1" :class "text-gray-11"}
      "Back"]]
    [:> Button {:variant "ghost" :color "gray" :onClick #(on-change (dissoc selected-values key))}
     [:> Text {:size "1" :class "text-gray-11"}
      "Clear"]]]

   [:> Separator {:class "w-full"}]

   [:> Text {:as "p" :size "1" :mt "3" :class "text-gray-11"}
    (get-display-key display-key)]

   [:div {:class "space-y-1 px-2 pt-3"}
    [:> Button {:variant "ghost"
                :color "gray"
                :class "w-full justify-between"
                :onClick #(on-change (assoc selected-values key values))}
     [:> Text {:size "2" :class "text-gray-12"}
      "Select all"]]

    (for [value values
          :let [is-selected (some #(= % value) (get selected-values key []))]]
      ^{:key (str key "-" value)}
      [:> Button {:variant "ghost"
                  :color "gray"
                  :class "w-full justify-between"
                  :onClick (fn [_]
                             (let [new-values (if is-selected
                                                (remove (fn [v] (= v value)) (get selected-values key []))
                                                (conj (or (get selected-values key []) []) value))
                                   new-selected (if (empty? new-values)
                                                  (dissoc selected-values key)
                                                  (assoc selected-values key new-values))]
                               (on-change new-selected)))}
       [:> Text {:size "2" :class "text-gray-12"}
        value]
       (when is-selected
         [:> Check {:size 16}])])]])

(defn keys-view [grouped-tags search-term selected-values on-change on-select-key]
  (let [filtered-keys (if (empty? search-term)
                        (keys grouped-tags)

                        (filter (fn [key]
                                  (let [display-key (get-key-name key)
                                        values (get grouped-tags key)]
                                    (or
                                     (cs/includes? (cs/lower-case display-key)
                                                   (cs/lower-case search-term))

                                     (some #(cs/includes?
                                             (cs/lower-case %)
                                             (cs/lower-case search-term))
                                           values))))
                                (keys grouped-tags)))]

    [:div {:class "w-full max-h-64 overflow-y-auto"}
     [:> Box {:class "space-y-1 p-2"}
      (for [key filtered-keys
            :let [display-key (get-key-name key)
                  selected-count (count (get selected-values key []))]]
        ^{:key key}
        [:> Button {:variant "ghost"
                    :color "gray"
                    :onClick #(on-select-key key)
                    :class "w-full justify-between gap-3"}
         [:> Text {:size "2" :class "truncate text-gray-12"}
          (get-display-key display-key)]
         [:> Flex {:align "center" :gap "2"}
          (when (pos? selected-count)
            [:> Box {:class "flex items-center justify-center rounded-full h-5 w-5 bg-gray-11"}
             [:> Text {:size "1" :weight "bold" :class "text-white"}
              selected-count]])
          [:> ChevronRight {:size 16}]]])]]))

(defn search-results-view [grouped-tags search-term selected-values on-change on-select-key]
  (let [search-results (for [[key values] grouped-tags
                             value values
                             :let [display-key (get-key-name key)
                                   key-match? (cs/includes? (cs/lower-case display-key)
                                                            (cs/lower-case search-term))
                                   value-match? (cs/includes? (cs/lower-case value)
                                                              (cs/lower-case search-term))]
                             :when (or key-match? value-match?)]
                         {:key key
                          :value value
                          :display-key display-key})]

    [:> Box {:class "w-full max-h-64 overflow-y-auto"}
     [:> Box {:class "space-y-1 p-2"}
      (for [{:keys [key value display-key]} search-results]
        ^{:key (str key "-" value)}
        [:> Button {:variant "ghost"
                    :color "gray"
                    :class "w-full justify-between gap-2"
                    :onClick #(on-select-key key)}
         [:> Flex {:direction "column" :align "start"}
          [:> Text {:size "2" :class "text-gray-12"} value]
          [:> Text {:size "1" :class "text-gray-11"} (str "Value | " (get-display-key display-key))]]
         [:> ChevronRight {:size 16}]])]]))

(defn tag-selector [selected-tags on-change]
  (let [all-tags (rf/subscribe [:connections->tags])
        loading? (rf/subscribe [:connections->tags-loading?])
        search-term (r/atom "")
        selected-values (r/atom (or selected-tags {}))
        current-view (r/atom :keys) ;; :keys, :values ou :search
        current-key (r/atom nil)]

    (fn [selected-tags on-change]
      (let [grouped-tags (group-tags-by-key @all-tags)
            has-search-results? (and (not-empty @search-term)
                                     (not= @current-view :values))]

        [:> Box
         (when (or (= @current-view :keys)
                   (= @current-view :search))
           [:div {:class "mb-3"}
            [searchbox/main
             {:value @search-term
              :on-change (fn [new-term]
                           (reset! search-term new-term)
                           (when (not-empty new-term)
                             (reset! current-view :search)
                             (reset! current-key nil)))
              :placeholder "Search Tags"
              :display-key :text
              :searchable-keys [:text]
              :hide-results-list true
              :size :small
              :variant :small}]])

         (cond
           (= @current-view :values)
           [values-view
            @current-key
            (get grouped-tags @current-key)
            (get-key-name @current-key)
            @selected-values
            (fn [new-values]
              (reset! selected-values new-values)
              (on-change new-values))
            (fn []
              (reset! current-view :keys)
              (reset! search-term ""))]

           has-search-results?
           [search-results-view grouped-tags @search-term @selected-values
            (fn [new-values]
              (reset! selected-values new-values)
              (on-change new-values))
            (fn [key]
              (reset! current-key key)
              (reset! current-view :values))]

           :else
           (do
             (when (not= @current-view :keys)
               (reset! current-view :keys))

             (if @loading?
               [:div {:class "text-center"} "Loading tags..."]
               [keys-view grouped-tags @search-term @selected-values
                (fn [new-values]
                  (reset! selected-values new-values)
                  (on-change new-values))
                (fn [key]
                  (reset! current-key key)
                  (reset! current-view :values))])))]))))
